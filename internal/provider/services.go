package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/noderings/terraform-provider-noderings/internal/service/billing"
	"github.com/noderings/terraform-provider-noderings/internal/service/catalog"
	"github.com/noderings/terraform-provider-noderings/internal/service/compute"
	"github.com/noderings/terraform-provider-noderings/internal/service/dns"
	"github.com/noderings/terraform-provider-noderings/internal/service/firewall"
	"github.com/noderings/terraform-provider-noderings/internal/service/gameserver"
	"github.com/noderings/terraform-provider-noderings/internal/service/iam"
	"github.com/noderings/terraform-provider-noderings/internal/service/marketplace"
)

// Each product package lists its resources and data sources here.
var serviceResources = [][]func() resource.Resource{
	dns.Resources(),
	firewall.Resources(),
	gameserver.Resources(),
	iam.Resources(),
	compute.Resources(),
}

var serviceDataSources = [][]func() datasource.DataSource{
	billing.DataSources(),
	catalog.DataSources(),
	dns.DataSources(),
	firewall.DataSources(),
	gameserver.DataSources(),
	iam.DataSources(),
	compute.DataSources(),
	marketplace.DataSources(),
}
