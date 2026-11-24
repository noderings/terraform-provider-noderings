// Package provider implements the NodeRings Terraform provider.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
)

// Environment variables read when the matching argument is not set.
var (
	envEndpoint = []string{"NODERINGS_API_URL"}
	envToken    = []string{"NODERINGS_TOKEN", "NODERINGS_API_TOKEN", "NR_API_TOKEN"}
	envOrg      = []string{"NODERINGS_ORGANIZATION_ID", "NR_ORGANIZATION_ID"}
)

var _ provider.Provider = &nodeRingsProvider{}

type nodeRingsProvider struct {
	version string
	// userAgent replaces the default User-Agent when set, so a wrapper such as
	// the Pulumi provider can identify itself to the API.
	userAgent string
}

type providerModel struct {
	Endpoint              types.String  `tfsdk:"endpoint"`
	Token                 types.String  `tfsdk:"token"`
	OrganizationID        types.String  `tfsdk:"organization_id"`
	MaxRetries            types.Int64   `tfsdk:"max_retries"`
	RequestsPerSecond     types.Float64 `tfsdk:"requests_per_second"`
	HTTPTimeout           types.String  `tfsdk:"http_timeout"`
	SkipOrganizationCheck types.Bool    `tfsdk:"skip_organization_check"`
}

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &nodeRingsProvider{version: version} }
}

// NewWithUserAgent is New with a fixed User-Agent for every API request.
func NewWithUserAgent(version, userAgent string) func() provider.Provider {
	return func() provider.Provider { return &nodeRingsProvider{version: version, userAgent: userAgent} }
}

func (p *nodeRingsProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "noderings"
	resp.Version = p.version
}

func (p *nodeRingsProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The NodeRings provider manages resources on the [NodeRings](https://noderings.com) cloud marketplace.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "API base URL. Defaults to `" + client.DefaultEndpoint + "`. Can also be set with `NODERINGS_API_URL`.",
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Service-account token. Can also be set with `NODERINGS_TOKEN` (or `NODERINGS_API_TOKEN`, `NR_API_TOKEN`).",
			},
			"organization_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Organization to act in, sent as `X-Organization-ID`. Defaults to the token's organization. Can also be set with `NODERINGS_ORGANIZATION_ID`.",
			},
			"max_retries": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "How many times to retry rate-limited and transient failures. Defaults to `25`.",
			},
			"requests_per_second": schema.Float64Attribute{
				Optional:            true,
				MarkdownDescription: "Client-side limit on requests per second for each API operation. Defaults to `2`, the API's own limit. Set `0` to disable.",
			},
			"http_timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Timeout for a single HTTP request, as a duration such as `60s`. Defaults to `60s`.",
			},
			"skip_organization_check": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip checking the token and organization when the provider is configured. Defaults to `false`.",
			},
		},
	}
}

func firstEnv(names []string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func (p *nodeRingsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for attr, v := range map[string]types.String{"endpoint": cfg.Endpoint, "token": cfg.Token, "organization_id": cfg.OrganizationID} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Unknown provider argument",
				fmt.Sprintf("%s must be known when the provider is configured. Set it from a variable or environment variable instead of another resource.", attr))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := valueOr(cfg.Endpoint, firstEnv(envEndpoint))
	token := valueOr(cfg.Token, firstEnv(envToken))
	org := valueOr(cfg.OrganizationID, firstEnv(envOrg))
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing NodeRings token",
			"Set the provider's token argument or the NODERINGS_TOKEN environment variable to a service-account token. Create one under Organization > Access Control > Service accounts.")
		return
	}

	maxRetries := 25
	if !cfg.MaxRetries.IsNull() && !cfg.MaxRetries.IsUnknown() {
		maxRetries = int(cfg.MaxRetries.ValueInt64())
	}
	rps := 2.0
	if !cfg.RequestsPerSecond.IsNull() && !cfg.RequestsPerSecond.IsUnknown() {
		rps = cfg.RequestsPerSecond.ValueFloat64()
	}
	timeout := 60 * time.Second
	if s := valueOr(cfg.HTTPTimeout, ""); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("http_timeout"), "Invalid http_timeout", fmt.Sprintf("%q is not a positive duration such as 60s.", s))
			return
		}
		timeout = d
	}

	userAgent := p.userAgent
	if userAgent == "" {
		userAgent = fmt.Sprintf("terraform-provider-noderings/%s (+https://registry.terraform.io/providers/noderings/noderings) Terraform/%s", p.version, req.TerraformVersion)
	}
	c, err := client.New(client.Config{
		Endpoint:          endpoint,
		Token:             token,
		OrganizationID:    org,
		UserAgent:         userAgent,
		MaxRetries:        maxRetries,
		RequestsPerSecond: rps,
		HTTPTimeout:       timeout,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	if !cfg.SkipOrganizationCheck.ValueBool() {
		orgs, err := c.ListOrganizations(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Could not authenticate with NodeRings", "Listing organizations failed: "+err.Error())
			return
		}
		if org != "" {
			found := false
			for _, o := range orgs {
				if o.ID == org {
					found = true
					break
				}
			}
			if !found {
				resp.Diagnostics.AddAttributeError(path.Root("organization_id"), "Organization not available",
					fmt.Sprintf("The token cannot act in organization %s.", org))
				return
			}
		}
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

func valueOr(v types.String, fallback string) string {
	if v.IsNull() || v.IsUnknown() || strings.TrimSpace(v.ValueString()) == "" {
		return fallback
	}
	return strings.TrimSpace(v.ValueString())
}

func (p *nodeRingsProvider) Resources(_ context.Context) []func() resource.Resource {
	var out []func() resource.Resource
	for _, rs := range serviceResources {
		out = append(out, rs...)
	}
	return out
}

func (p *nodeRingsProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	var out []func() datasource.DataSource
	for _, ds := range serviceDataSources {
		out = append(out, ds...)
	}
	return out
}
