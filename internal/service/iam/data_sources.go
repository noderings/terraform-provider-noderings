package iam

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var publicKeyRE = regexp.MustCompile(`^\s*(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/]+=*( .*)?\s*$`)

// --- noderings_ssh_key data source ---

var _ datasource.DataSourceWithConfigValidators = &sshKeyDataSource{}

// NewSSHKeyDataSource returns the noderings_ssh_key data source.
func NewSSHKeyDataSource() datasource.DataSource { return &sshKeyDataSource{} }

type sshKeyDataSource struct{ client *client.Client }

func (d *sshKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *sshKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an SSH key by ID, name or fingerprint.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Key ID."},
			"name":        schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Key name (case-insensitive)."},
			"fingerprint": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "SHA-256 fingerprint as reported by NodeRings."},
			"public_key":  schema.StringAttribute{Computed: true, MarkdownDescription: "OpenSSH public key."},
			"key_type":    schema.StringAttribute{Computed: true, MarkdownDescription: "Key algorithm."},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
		},
	}
}

func (d *sshKeyDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"), path.MatchRoot("fingerprint")),
	}
}

func (d *sshKeyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *sshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m sshKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var key *client.SSHKey
	if id := m.ID.ValueString(); id != "" {
		k, err := d.client.GetSSHKey(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading SSH key", err.Error())
			return
		}
		key = k
	} else {
		keys, err := d.client.ListSSHKeys(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Error listing SSH keys", err.Error())
			return
		}
		for i := range keys {
			k := &keys[i]
			if (!m.Name.IsNull() && strings.EqualFold(k.Name, m.Name.ValueString())) ||
				(!m.Fingerprint.IsNull() && k.Fingerprint == m.Fingerprint.ValueString()) {
				key = k
				break
			}
		}
		if key == nil {
			resp.Diagnostics.AddError("SSH key not found", "No SSH key in the organization matches the given name or fingerprint.")
			return
		}
	}
	m.PublicKey = types.StringNull()
	setSSHKey(&m, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_organization data source ---

// NewOrganizationDataSource returns the noderings_organization data source.
func NewOrganizationDataSource() datasource.DataSource { return &organizationDataSource{} }

type organizationDataSource struct{ client *client.Client }

type organizationModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Type        types.String `tfsdk:"type"`
	Verified    types.Bool   `tfsdk:"verified"`
}

func (d *organizationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *organizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organization the provider acts in.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Organization ID."},
			"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Organization name."},
			"description": schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
			"type":        schema.StringAttribute{Computed: true, MarkdownDescription: "`client` (buys infrastructure) or `provider` (sells it)."},
			"verified":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether NodeRings has verified the organization."},
		},
	}
}

func (d *organizationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *organizationDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	org, err := d.client.GetOrganization(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading organization", fmt.Sprintf("%s\n\nThe token's role needs the OrganizationService::GetOrganization permission.", err))
		return
	}
	m := organizationModel{
		ID:          types.StringValue(org.ID),
		Name:        types.StringValue(org.Name),
		Description: types.StringValue(org.Description),
		Type:        types.StringValue(framework.TrimEnum(org.Type, "ORGANIZATION_TYPE_")),
		Verified:    types.BoolValue(org.IsVerified),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
