// Package iam implements SSH keys, roles, groups, role assignments, secrets,
// environment variables and organization and user lookups.
package iam

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// Resources lists this package's resources.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{NewSSHKeyResource,
		NewRoleResource, NewGroupResource, NewGroupMembershipResource, NewRoleAssignmentResource, NewSecretResource, NewEnvironmentVariableResource}
}

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewSSHKeyDataSource, NewOrganizationDataSource,
		NewPermissionDataSource, NewPermissionsDataSource, NewRoleDataSource, NewRolesDataSource, NewUsersDataSource}
}

var (
	_ resource.Resource                = &sshKeyResource{}
	_ resource.ResourceWithImportState = &sshKeyResource{}
)

// NewSSHKeyResource returns the noderings_ssh_key resource.
func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ client *client.Client }

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	PublicKey   types.String `tfsdk:"public_key"`
	KeyType     types.String `tfsdk:"key_type"`
	Fingerprint types.String `tfsdk:"fingerprint"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SSH public key that virtual machines can be created with. Keys cannot be changed in place; changing any argument replaces the key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Key ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name, unique in the organization (case-insensitive).",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255), framework.SafeString()},
			},
			"public_key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "OpenSSH public key, for example the contents of `~/.ssh/id_ed25519.pub`.",
				PlanModifiers:       []planmodifier.String{sameKeyKeepsState{}, stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(publicKeyRE, "must be an OpenSSH public key such as \"ssh-ed25519 AAAA... comment\"")},
			},
			"key_type":    schema.StringAttribute{Computed: true, MarkdownDescription: "Key algorithm: `ed25519`, `rsa`, `ecdsa` or `dsa`.", PlanModifiers: keep},
			"fingerprint": schema.StringAttribute{Computed: true, MarkdownDescription: "SHA-256 fingerprint as reported by NodeRings.", PlanModifiers: keep},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.client.CreateSSHKey(ctx, plan.Name.ValueString(), strings.TrimSpace(plan.PublicKey.ValueString()))
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nA key with the same name or fingerprint already exists. Import it with: terraform import <address> <key id>"
		}
		resp.Diagnostics.AddError("Error creating SSH key", detail)
		return
	}
	setSSHKey(&plan, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.client.GetSSHKey(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading SSH key", err.Error())
		return
	}
	setSSHKey(&state, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update is never called: every argument requires replacement.
func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSSHKey(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting SSH key", err.Error())
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// setSSHKey copies API values into the model. The API stores a canonical
// single-line key, so the configured spelling is kept when it is the same key.
func setSSHKey(m *sshKeyModel, k *client.SSHKey) {
	m.ID = types.StringValue(k.ID)
	m.Name = types.StringValue(k.Name)
	if m.PublicKey.IsNull() || m.PublicKey.IsUnknown() || !sameKey(m.PublicKey.ValueString(), k.PublicKey) {
		m.PublicKey = types.StringValue(k.PublicKey)
	}
	m.KeyType = types.StringValue(k.KeyType)
	m.Fingerprint = types.StringValue(k.Fingerprint)
	m.CreatedAt = framework.Time(k.Timestamps.CreateTime)
}

// sameKeyKeepsState plans the stored public_key when the configured one is the
// same key spelled differently (a trailing newline from file(), another
// comment), so that does not replace the key. It matters after import, when
// the state holds the API's spelling rather than the configuration's.
type sameKeyKeepsState struct{}

func (sameKeyKeepsState) Description(context.Context) string {
	return "Keeps the stored key when only spacing or the comment differ."
}

func (m sameKeyKeepsState) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (sameKeyKeepsState) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if sameKey(req.PlanValue.ValueString(), req.StateValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}

// sameKey compares the algorithm and key material, ignoring comments and spacing.
func sameKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	return len(fa) >= 2 && len(fb) >= 2 && fa[0] == fb[0] && fa[1] == fb[1]
}
