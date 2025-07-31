// SPDX-FileCopyrightText: 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/sharetypes"
	"github.com/gophercloud/gophercloud/v2/pagination"
)

// TODO: This type is imported from gophercloud, because the "IsPublic" JSON key was changed between OpenStack microversions, but remained the same in gophercloud.
type ShareType struct {
	// The Share Type ID
	ID string `json:"id"`
	// The Share Type name
	Name string `json:"name"`
	// Indicates whether a share type is publicly accessible
	IsPublic bool `json:"share_type_access:is_public"`
	// The required extra specifications for the share type
	RequiredExtraSpecs map[string]any `json:"required_extra_specs"`
	// The extra specifications for the share type
	ExtraSpecs map[string]any `json:"extra_specs"`
}

// ExtractShareTypes extracts and returns ShareTypes. It is used while
// iterating over a sharetypes.List call.
func ExtractShareTypes(r pagination.Page) ([]ShareType, error) {
	var s struct {
		ShareTypes []ShareType `json:"share_types"`
	}
	err := (r.(sharetypes.ShareTypePage)).ExtractInto(&s)
	return s.ShareTypes, err
}
