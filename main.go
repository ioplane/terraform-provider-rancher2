package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-mux/tf5to6server"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/rancher/terraform-provider-rancher2/rancher2"
)

func main() {
	server, err := tf5to6server.UpgradeServer(context.Background(), rancher2.Provider().GRPCProvider)
	if err != nil {
		log.Fatal(err)
	}
	plugin.Serve(&plugin.ServeOpts{
		GRPCProviderV6Func: func() tfprotov6.ProviderServer { return server },
		ProviderAddr:       "registry.terraform.io/rancher/rancher2",
	})
}
