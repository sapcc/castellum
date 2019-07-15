// Package resources provides integration between Limes and Gophercloud.
package resources

import (
	"github.com/gophercloud/gophercloud"
)

// NewLimesV1 creates a ServiceClient that may be used to interact with Limes.
func NewLimesV1(client *gophercloud.ProviderClient, endpointOpts gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	endpointOpts.ApplyDefaults("resources")
	endpoint, err := client.EndpointLocator(endpointOpts)
	if err != nil {
		return nil, err
	}

	endpoint += "v1/"

	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       endpoint,
		Type:           "resources",
	}, nil
}
