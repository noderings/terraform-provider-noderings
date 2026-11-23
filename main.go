package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/noderings/terraform-provider-noderings/internal/provider"
)

// version is set by GoReleaser at release time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/noderings/noderings",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
