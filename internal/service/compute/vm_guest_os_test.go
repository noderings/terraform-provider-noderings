package compute_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"golang.org/x/crypto/ssh"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

// defaultGuestDistros is every distro the platform offers. Override with
// NODERINGS_ACC_OS_MATRIX_DISTROS (comma separated).
var defaultGuestDistros = []string{
	"ubuntu_26_04", "ubuntu_24_04", "ubuntu_22_04",
	"debian_12", "debian_13",
	"fedora_44", "fedora_43",
	"centos_10", "centos_9",
	"almalinux_10", "almalinux_9",
	"rocky_10", "rocky_9",
}

// guestProbe prints one key=value per line about the running guest.
const guestProbe = `cloud-init status --wait >/dev/null 2>&1
. /etc/os-release
echo "id=$ID"
echo "version=$VERSION_ID"
echo "machine=$(cat /etc/machine-id)"
echo "hostname=$(hostname)"
echo "cloudinit=$(cloud-init status 2>/dev/null | tr -d ' ' | tr '\n' ',')"
echo "net4=$(ping -c1 -W3 1.1.1.1 >/dev/null 2>&1 && echo ok || echo fail)"
echo "net6=$(ping -6 -c1 -W3 2606:4700:4700::1111 >/dev/null 2>&1 && echo ok || echo fail)"
echo "dns=$(getent hosts noderings.com >/dev/null && echo ok || echo fail)"`

// TestVirtualMachineGuestOSMatrix reinstalls one VM with every distro and
// checks, over SSH, that the guest really runs it and that cloud-init set it
// up: hostname, root SSH key, IPv4, DNS and (with NODERINGS_ACC_REQUIRE_IPV6=1)
// IPv6. The API alone cannot show this: it reports the requested distro even
// when the guest was not rebuilt.
//
// Live only, and opt-in because it keeps a billed VM for about an hour:
//
//	TF_ACC=1 NODERINGS_ACC=1 NODERINGS_ACC_OS_MATRIX=1 \
//	NODERINGS_ACC_VM_PLAN_CODE=... NODERINGS_ACC_VM_COUNTRY=... NODERINGS_ACC_VM_CITY=... \
//	go test ./internal/service/compute -run TestVirtualMachineGuestOSMatrix -timeout 3h -v
func TestVirtualMachineGuestOSMatrix(t *testing.T) {
	if !acctest.Live() || os.Getenv("NODERINGS_ACC_OS_MATRIX") != "1" {
		t.Skip("set NODERINGS_ACC_OS_MATRIX=1 (with a live run) to reinstall a VM through every distro")
	}
	distros := defaultGuestDistros
	if v := os.Getenv("NODERINGS_ACC_OS_MATRIX_DISTROS"); v != "" {
		distros = strings.Split(v, ",")
	}
	requireIPv6 := os.Getenv("NODERINGS_ACC_REQUIRE_IPV6") == "1"

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

	env := acctest.New(t)
	name := acctest.RandomName("tf-acc-os")
	previousMachine := ""
	steps := make([]resource.TestStep, 0, len(distros))
	for _, distro := range distros {
		distro := strings.TrimSpace(distro)
		steps = append(steps, resource.TestStep{
			Config: env.Config(fmt.Sprintf(`
resource "noderings_ssh_key" "test" {
  name       = %q
  public_key = %q
}
`, name, authorizedKey) + vmConfig(name, fmt.Sprintf(`
  distro              = %q
  ssh_key_ids         = [noderings_ssh_key.test.id]
  reinstall_on_change = true`, distro))),
			Check: func(s *terraform.State) error {
				ip := s.RootModule().Resources[vmAddr].Primary.Attributes["ipv4"]
				facts, err := probeGuest(ip, signer, distro, 12*time.Minute)
				if err != nil {
					return fmt.Errorf("%s: %w", distro, err)
				}
				t.Logf("%-13s %s %s cloud-init=%s hostname=%s ipv4=%s ipv6=%s dns=%s",
					distro, facts["id"], facts["version"], facts["cloudinit"], facts["hostname"], facts["net4"], facts["net6"], facts["dns"])
				var problems []string
				if !strings.Contains(facts["cloudinit"], "done") {
					problems = append(problems, "cloud-init did not finish cleanly: "+facts["cloudinit"])
				}
				if facts["hostname"] != name {
					problems = append(problems, fmt.Sprintf("hostname is %q, want %q", facts["hostname"], name))
				}
				if facts["net4"] != "ok" {
					problems = append(problems, "no IPv4 connectivity")
				}
				if facts["dns"] != "ok" {
					problems = append(problems, "DNS does not resolve")
				}
				if requireIPv6 && facts["net6"] != "ok" {
					problems = append(problems, "no IPv6 connectivity")
				}
				if previousMachine != "" && facts["machine"] == previousMachine {
					problems = append(problems, "machine-id did not change, so the guest was not reinstalled")
				}
				previousMachine = facts["machine"]
				if len(problems) > 0 {
					return fmt.Errorf("%s: %s", distro, strings.Join(problems, "; "))
				}
				return nil
			},
		})
	}
	env.Run(t, resource.TestCase{Steps: steps})
}

// guestOS maps a distro name such as "ubuntu_24_04" to the os-release ID and
// VERSION_ID prefix the guest must report ("ubuntu", "24.04").
func guestOS(distro string) (string, string) {
	m := regexp.MustCompile(`^([a-z]+)_(\d+)(?:_(\d+))?$`).FindStringSubmatch(distro)
	if m == nil {
		return distro, ""
	}
	version := m[2]
	if m[3] != "" {
		version += "." + m[3]
	}
	return m[1], version
}

// probeGuest logs in as root until the guest reports the wanted distro, then
// returns its facts.
func probeGuest(ip string, signer ssh.Signer, distro string, timeout time.Duration) (map[string]string, error) {
	wantID, wantVersion := guestOS(distro)
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // fresh guest, new host key on every reinstall
		Timeout:         10 * time.Second,
	}
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		facts, err := runProbe(ip, cfg)
		if err != nil {
			last = err.Error()
		} else if facts["id"] == wantID && (wantVersion == "" || facts["version"] == wantVersion || strings.HasPrefix(facts["version"], wantVersion+".")) {
			return facts, nil
		} else {
			last = fmt.Sprintf("guest still runs %s %s", facts["id"], facts["version"])
		}
		time.Sleep(15 * time.Second)
	}
	return nil, fmt.Errorf("guest did not come up as %s %s within %s (last: %s)", wantID, wantVersion, timeout, last)
}

func runProbe(ip string, cfg *ssh.ClientConfig) (map[string]string, error) {
	client, err := ssh.Dial("tcp", ip+":22", cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	out, err := session.CombinedOutput(guestProbe)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	facts := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			facts[k] = v
		}
	}
	return facts, nil
}

func TestGuestOSMapping(t *testing.T) {
	for distro, want := range map[string][2]string{
		"ubuntu_24_04": {"ubuntu", "24.04"},
		"debian_13":    {"debian", "13"},
		"almalinux_10": {"almalinux", "10"},
		"centos_9":     {"centos", "9"},
		"rocky_10":     {"rocky", "10"},
	} {
		if id, version := guestOS(distro); id != want[0] || version != want[1] {
			t.Errorf("guestOS(%q) = %q, %q; want %q, %q", distro, id, version, want[0], want[1])
		}
	}
}
