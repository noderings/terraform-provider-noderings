package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/noderings/terraform-provider-noderings/internal/service/catalog"
	"github.com/noderings/terraform-provider-noderings/internal/service/dns"
	"github.com/noderings/terraform-provider-noderings/internal/service/gameserver"
	"github.com/noderings/terraform-provider-noderings/internal/service/iam"
)

// Each product package lists its resources and data sources here.
var serviceResources = [][]func() resource.Resource{
	dns.Resources(),
	gameserver.Resources(),
	iam.Resources(),
}

var serviceDataSources = [][]func() datasource.DataSource{
	catalog.DataSources(),
	dns.DataSources(),
	gameserver.DataSources(),
	iam.DataSources(),
}
