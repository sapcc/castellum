// Copyright 2020 SAP SE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package gophercloud-sapcc provides integration between SAP CC services and
// Gophercloud.
package clients

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

// NewAutomationV1 creates a ServiceClient that may be used with the v1 automation package.
func NewAutomationV1(client *gophercloud.ProviderClient, endpointOpts gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc := new(gophercloud.ServiceClient)
	endpointOpts.ApplyDefaults("automation")
	url, err := client.EndpointLocator(endpointOpts)
	if err != nil {
		return sc, err
	}

	resourceBase := url + "api/v1/"
	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       url,
		Type:           "automation",
		ResourceBase:   resourceBase,
	}, nil
}

// NewHermesV1 creates a ServiceClient that may be used with the v1 hermes package.
func NewHermesV1(client *gophercloud.ProviderClient, endpointOpts gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc := new(gophercloud.ServiceClient)
	endpointOpts.ApplyDefaults("audit-data")
	url, err := client.EndpointLocator(endpointOpts)
	if err != nil {
		return sc, err
	}

	resourceBase := url // TODO: check the slash: + "/"
	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       url,
		Type:           "audit-data",
		ResourceBase:   resourceBase,
	}, nil
}

// NewBilling creates a ServiceClient that may be used with the billing package.
func NewBilling(client *gophercloud.ProviderClient, endpointOpts gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc := new(gophercloud.ServiceClient)
	endpointOpts.ApplyDefaults("sapcc-billing")
	url, err := client.EndpointLocator(endpointOpts)
	if err != nil {
		return sc, err
	}

	resourceBase := url
	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       url,
		Type:           "sapcc-billing",
		ResourceBase:   resourceBase,
	}, nil
}

// NewArcV1 creates a ServiceClient that may be used with the v1 arc package.
func NewArcV1(client *gophercloud.ProviderClient, endpointOpts gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc := new(gophercloud.ServiceClient)
	endpointOpts.ApplyDefaults("arc")
	url, err := client.EndpointLocator(endpointOpts)
	if err != nil {
		return sc, err
	}

	resourceBase := url + "api/v1/"
	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       url,
		Type:           "arc",
		ResourceBase:   resourceBase,
	}, nil
}
