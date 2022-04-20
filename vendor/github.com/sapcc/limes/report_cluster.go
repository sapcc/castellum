/*******************************************************************************
*
* Copyright 2017-2020 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package limes

import (
	"encoding/json"
	"sort"
)

//ClusterReport contains aggregated data about resource usage in a cluster.
//It is returned by GET endpoints for clusters.
type ClusterReport struct {
	ID           string                `json:"id"`
	Services     ClusterServiceReports `json:"services"`
	MaxScrapedAt *int64                `json:"max_scraped_at,omitempty"`
	MinScrapedAt *int64                `json:"min_scraped_at,omitempty"`
}

//ClusterServiceReport is a substructure of ClusterReport containing data for
//a single backend service.
type ClusterServiceReport struct {
	ServiceInfo
	Resources         ClusterResourceReports  `json:"resources"`
	Rates             ClusterRateLimitReports `json:"rates,omitempty"`
	MaxScrapedAt      *int64                  `json:"max_scraped_at,omitempty"`
	MinScrapedAt      *int64                  `json:"min_scraped_at,omitempty"`
	MaxRatesScrapedAt *int64                  `json:"max_rates_scraped_at,omitempty"`
	MinRatesScrapedAt *int64                  `json:"min_rates_scraped_at,omitempty"`
}

//ClusterResourceReport is a substructure of ClusterReport containing data for
//a single resource.
type ClusterResourceReport struct {
	//Several fields are pointers to values to enable precise control over which fields are rendered in output.
	ResourceInfo
	Capacity      *uint64                        `json:"capacity,omitempty"`
	RawCapacity   *uint64                        `json:"raw_capacity,omitempty"`
	CapacityPerAZ ClusterAvailabilityZoneReports `json:"per_availability_zone,omitempty"`
	DomainsQuota  *uint64                        `json:"domains_quota,omitempty"`
	Usage         uint64                         `json:"usage"`
	BurstUsage    uint64                         `json:"burst_usage,omitempty"`
	PhysicalUsage *uint64                        `json:"physical_usage,omitempty"`
	Subcapacities JSONString                     `json:"subcapacities,omitempty"`
}

//ClusterAvailabilityZoneReport is a substructure of ClusterResourceReport containing
//capacity and usage data for a single resource in an availability zone.
type ClusterAvailabilityZoneReport struct {
	Name        string `json:"name"`
	Capacity    uint64 `json:"capacity"`
	RawCapacity uint64 `json:"raw_capacity,omitempty"`
	Usage       uint64 `json:"usage,omitempty"`
}

// ClusterRateLimitReport is the structure for rate limits per target type URI and their rate limited actions.
type ClusterRateLimitReport struct {
	RateInfo
	Limit  uint64 `json:"limit,omitempty"`
	Window Window `json:"window,omitempty"`
}

//ClusterServiceReports provides fast lookup of services by service type, but
//serializes to JSON as a list.
type ClusterServiceReports map[string]*ClusterServiceReport

//MarshalJSON implements the json.Marshaler interface.
func (s ClusterServiceReports) MarshalJSON() ([]byte, error) {
	//serialize with ordered keys to ensure testcase stability
	types := make([]string, 0, len(s))
	for typeStr := range s {
		types = append(types, typeStr)
	}
	sort.Strings(types)
	list := make([]*ClusterServiceReport, len(s))
	for idx, typeStr := range types {
		list[idx] = s[typeStr]
	}
	return json.Marshal(list)
}

//UnmarshalJSON implements the json.Unmarshaler interface
func (s *ClusterServiceReports) UnmarshalJSON(b []byte) error {
	tmp := make([]*ClusterServiceReport, 0)
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	t := make(ClusterServiceReports)
	for _, cs := range tmp {
		t[cs.Type] = cs
	}
	*s = t
	return nil
}

//ClusterResourceReports provides fast lookup of resources by resource name,
//but serializes to JSON as a list.
type ClusterResourceReports map[string]*ClusterResourceReport

//MarshalJSON implements the json.Marshaler interface.
func (r ClusterResourceReports) MarshalJSON() ([]byte, error) {
	//serialize with ordered keys to ensure testcase stability
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]*ClusterResourceReport, len(r))
	for idx, name := range names {
		list[idx] = r[name]
	}
	return json.Marshal(list)
}

//UnmarshalJSON implements the json.Unmarshaler interface
func (r *ClusterResourceReports) UnmarshalJSON(b []byte) error {
	tmp := make([]*ClusterResourceReport, 0)
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	t := make(ClusterResourceReports)
	for _, cr := range tmp {
		t[cr.Name] = cr
	}
	*r = t
	return nil
}

//ClusterAvailabilityZoneReports provides fast lookup of availability zones
//using a map, but serializes to JSON as a list.
type ClusterAvailabilityZoneReports map[string]*ClusterAvailabilityZoneReport

//MarshalJSON implements the json.Marshaler interface.
func (r ClusterAvailabilityZoneReports) MarshalJSON() ([]byte, error) {
	//serialize with ordered keys to ensure testcase stability
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]*ClusterAvailabilityZoneReport, len(r))
	for idx, name := range names {
		list[idx] = r[name]
	}
	return json.Marshal(list)
}

//UnmarshalJSON implements the json.Unmarshaler interface
func (r *ClusterAvailabilityZoneReports) UnmarshalJSON(b []byte) error {
	tmp := make([]*ClusterAvailabilityZoneReport, 0)
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	t := make(ClusterAvailabilityZoneReports)
	for _, cr := range tmp {
		t[cr.Name] = cr
	}
	*r = t
	return nil
}

//ClusterRateLimitReports provides fast lookup of global rate limits using a map, but serializes
//to JSON as a list.
type ClusterRateLimitReports map[string]*ClusterRateLimitReport

//MarshalJSON implements the json.Marshaler interface.
func (r ClusterRateLimitReports) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]*ClusterRateLimitReport, len(r))
	for idx, name := range names {
		list[idx] = r[name]
	}
	return json.Marshal(list)
}

//UnmarshalJSON implements the json.Unmarshaler interface.
func (r *ClusterRateLimitReports) UnmarshalJSON(b []byte) error {
	tmp := make([]*ClusterRateLimitReport, 0)
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	t := make(ClusterRateLimitReports)
	for _, prl := range tmp {
		t[prl.Name] = prl
	}
	*r = t
	return nil
}
