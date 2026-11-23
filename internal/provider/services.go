package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/noderings/terraform-provider-noderings/internal/service/billing"
	"github.com/noderings/terraform-provider-noderings/internal/service/catalog"
	"github.com/noderings/terraform-provider-noderings/internal/service/dns"
	"github.com/noderings/terraform-provider-noderings/internal/service/iam"
)

// Each product package lists its resources and data sources here.
var serviceResources = [][]func() resource.Resource{
	dns.Resources(),
	iam.Resources(),
}

var serviceDataSources = [][]func() datasource.DataSource{
	billing.DataSources(),
	catalog.DataSources(),
	dns.DataSources(),
	iam.DataSources(),
}
