package iam

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

// The API lower-cases keys and silently drops other characters, so only
// keys it stores unchanged are accepted.
var (
	secretKeyRE = regexp.MustCompile(`^[a-z0-9_-]+$`)
	envKeyRE    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

var (
	_ resource.Resource                = &secretResource{}
	_ resource.ResourceWithImportState = &secretResource{}
)

// NewSecretResource returns the noderings_secret resource.
func NewSecretResource() resource.Resource { return &secretResource{} }

type secretResource struct{ client *client.Client }

type secretModel struct {
	ID        types.String `tfsdk:"id"`
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An organization secret. The API never returns a secret's value, so Terraform cannot detect changes made to it outside Terraform. " +
			"The value is stored in the Terraform state; protect the state accordingly.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Secret ID.", PlanModifiers: keep},
			"key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Key: lower-case letters, digits, `_` and `-`. Unique in the organization. Changing it replaces the secret.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(secretKeyRE, "must contain only lower-case letters, digits, _ and -"),
				},
			},
			"value": schema.StringAttribute{
				Required: true, Sensitive: true,
				MarkdownDescription: "Value, 1 to 255 characters. Changing it updates the secret in place.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.CreateSecret(ctx, plan.Key.ValueString(), plan.Value.ValueString())
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nA secret with this key already exists. Import it with: terraform import <address> <secret id>"
		}
		resp.Diagnostics.AddError("Error creating secret", detail)
		return
	}
	plan.ID = types.StringValue(s.ID)
	plan.Key = types.StringValue(s.Key)
	plan.CreatedAt = framework.Time(s.Timestamps.CreateTime)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetSecret(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading secret", err.Error())
		return
	}
	// The value is never returned, so the one in state is kept.
	state.Key = types.StringValue(s.Key)
	state.CreatedAt = framework.Time(s.Timestamps.CreateTime)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateSecret(ctx, state.ID.ValueString(), plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error updating secret", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSecret(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting secret", err.Error())
	}
}

// ImportState takes a secret ID. The value cannot be read, so the next apply
// writes the configured value.
func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- noderings_environment_variable ---

var (
	_ resource.Resource                = &envVarResource{}
	_ resource.ResourceWithImportState = &envVarResource{}
)

// NewEnvironmentVariableResource returns the noderings_environment_variable resource.
func NewEnvironmentVariableResource() resource.Resource { return &envVarResource{} }

type envVarResource struct{ client *client.Client }

type envVarModel struct {
	ID        types.String `tfsdk:"id"`
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *envVarResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment_variable"
}

func (r *envVarResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An organization environment variable. Its value is not secret: anyone who can read the organization's variables can see it. Use `noderings_secret` for credentials.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Variable ID.", PlanModifiers: keep},
			"key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Key: lower-case letters, digits and `_`. Unique in the organization. Changing it replaces the variable.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(envKeyRE, "must contain only lower-case letters, digits and _"),
				},
			},
			"value": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Value, 1 to 32767 characters. Changing it updates the variable in place.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 32767)},
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *envVarResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func setEnvVar(m *envVarModel, v *client.EnvironmentVariable) {
	m.ID = types.StringValue(v.ID)
	m.Key = types.StringValue(v.Key)
	m.Value = types.StringValue(v.Value)
	m.CreatedAt = framework.Time(v.Timestamps.CreateTime)
}

func (r *envVarResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan envVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.client.CreateEnvironmentVariable(ctx, plan.Key.ValueString(), plan.Value.ValueString())
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nA variable with this key already exists. Import it with: terraform import <address> <variable id>"
		}
		resp.Diagnostics.AddError("Error creating environment variable", detail)
		return
	}
	setEnvVar(&plan, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *envVarResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state envVarModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.client.GetEnvironmentVariable(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading environment variable", err.Error())
		return
	}
	setEnvVar(&state, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *envVarResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state envVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.client.UpdateEnvironmentVariable(ctx, id, plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error updating environment variable", err.Error())
		return
	}
	v, err := r.client.GetEnvironmentVariable(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading environment variable", err.Error())
		return
	}
	setEnvVar(&plan, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *envVarResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state envVarModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteEnvironmentVariable(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting environment variable", err.Error())
	}
}

func (r *envVarResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
