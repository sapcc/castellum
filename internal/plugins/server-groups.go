/******************************************************************************
*
*  Copyright 2021 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package plugins

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servergroups"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/keymanager/v1/secrets"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/pools"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
	"github.com/sapcc/go-bits/osext"
	"github.com/sapcc/go-bits/promquery"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

const (
	// ServerDeletionTimeout is how long the "server-groups" asset manager will
	// wait for servers to be deleted before reporting an error.
	ServerDeletionTimeout = 3 * time.Minute
	// ServerCreationTimeout is how long the "server-groups" asset manager will
	// wait for new servers to go into state ACTIVE before reporting an error.
	ServerCreationTimeout = 5 * time.Minute
	// ServerPollInterval is how often servers are polled during state transitions
	// when the desired state has not been reached yet.
	ServerPollInterval = 10 * time.Second
)

// NOTE 1: The `virtualmachine` labels look like `$NAME ($ID)` or just `$ID`, the
// latter without parentheses around the ID.
//
// NOTE 2: These queries return fractional values in the range 0..1, NOT percentages in the range 0..100.
var serverUsageQueries = map[castellum.UsageMetric]string{
	"cpu": `vrops_virtualmachine_cpu_usage_ratio{virtualmachine=~".*${ID}.*"} / 100`,
	"ram": `vrops_virtualmachine_memory_consumed_kilobytes{virtualmachine=~".*${ID}.*"} / vrops_virtualmachine_memory_kilobytes{virtualmachine=~".*${ID}.*"}`,
}

type assetManagerServerGroups struct {
	Provider       core.ProviderClient
	Prometheus     promquery.Client
	LocalRoleNames []string
}

func init() {
	core.AssetManagerRegistry.Add(func() core.AssetManager { return &assetManagerServerGroups{} })
}

// PluginTypeID implements the core.AssetManager interface.
func (m *assetManagerServerGroups) PluginTypeID() string { return "server-groups" }

// Init implements the core.AssetManager interface.
func (m *assetManagerServerGroups) Init(provider core.ProviderClient) (err error) {
	m.Provider = provider

	m.Prometheus, err = promquery.ConfigFromEnv("CASTELLUM_SERVERGROUPS_PROMETHEUS").Connect()
	if err != nil {
		return err
	}

	localRoleNamesStr := osext.MustGetenv("CASTELLUM_SERVERGROUPS_LOCAL_ROLES")
	for _, roleName := range strings.Split(localRoleNamesStr, ",") {
		roleName = strings.TrimSpace(roleName)
		if roleName != "" {
			m.LocalRoleNames = append(m.LocalRoleNames, roleName)
		}
	}

	return nil
}

// InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerServerGroups) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if strings.HasPrefix(string(assetType), "server-group:") {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []castellum.UsageMetric{"cpu", "ram"},
		}
	}
	return nil
}

// CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerServerGroups) CheckResourceAllowed(ctx context.Context, assetType db.AssetType, scopeUUID, configJSON string, existingResources map[db.AssetType]struct{}) error {
	// check that the server group exists and is in the right project
	groupID := strings.TrimPrefix(string(assetType), "server-group:")
	group, err := m.getServerGroup(ctx, groupID)
	if gophercloud.ResponseCodeIs(err, http.StatusNotFound) || (err == nil && group.ProjectID != scopeUUID) {
		return fmt.Errorf("server group not found in Nova: %s", groupID)
	}
	if err != nil {
		return err
	}

	// check that the config is valid
	_, err = m.parseAndValidateConfig(configJSON)
	return err
}

// ListAssets implements the core.AssetManager interface.
func (m *assetManagerServerGroups) ListAssets(_ context.Context, res db.Resource) ([]string, error) {
	groupUUID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	return []string{groupUUID}, nil
}

// GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerServerGroups) GetAssetStatus(ctx context.Context, res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return core.AssetStatus{}, err
	}

	groupID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	group, err := m.getServerGroup(ctx, groupID)
	if err != nil {
		return core.AssetStatus{}, fmt.Errorf("cannot GET server group: %w", err)
	}

	// check instance status
	isNewServer := make(map[string]bool)
	for _, serverID := range group.Members {
		server, err := servers.Get(ctx, computeV2, serverID).Extract()
		if err != nil {
			return core.AssetStatus{}, fmt.Errorf("cannot inspect server %s: %w", serverID, err)
		}
		// if any instance is not in a running state, that's a huge red flag and we
		// should not attempt any autoscaling until all servers are back into a
		// running state
		if server.Status == "ERROR" || server.Status == "SHUTOFF" {
			return core.AssetStatus{}, fmt.Errorf("server %s is in status %s", serverID, server.Status)
		}
		// for new servers, we will be more lenient wrt metric availability
		if time.Since(server.Created) < 10*time.Minute {
			isNewServer[server.ID] = true
		}
	}

	// get usage values for all servers
	result := core.AssetStatus{
		Size:  uint64(len(group.Members)),
		Usage: make(castellum.UsageValues),
	}
	for metric := range serverUsageQueries {
		result.Usage[metric] = 0
	}
	for _, serverID := range group.Members {
		for metric, queryTemplate := range serverUsageQueries {
			queryStr := strings.ReplaceAll(queryTemplate, "${ID}", serverID)
			value, err := m.Prometheus.GetSingleValue(ctx, queryStr, nil)
			if promquery.IsErrNoRows(err) && isNewServer[serverID] {
				// within a few minutes of instance creation, it's not a hard error if
				// the vrops metric has not showed up in Prometheus yet; we'll just
				// assume zero usage for now, which should be okay since downscaling
				// usually has a delay of way more than those few minutes
				value = 0
			} else if err != nil {
				return core.AssetStatus{}, err
			}
			if value < 0 {
				return core.AssetStatus{}, fmt.Errorf("expected value between 0..1, but got negative value from Prometheus query: %s", queryStr)
			}
			if value > 1 {
				return core.AssetStatus{}, fmt.Errorf("expected value between 0..1, but got larger value from Prometheus query: %s", queryStr)
			}
			result.Usage[metric] += value
		}
	}

	return result, nil
}

// SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerServerGroups) SetAssetSize(ctx context.Context, res db.Resource, assetUUID string, _, newSize uint64) (castellum.OperationOutcome, error) {
	cfg, err := m.parseAndValidateConfig(res.ConfigJSON)
	if err != nil {
		// if validation fails here, we should not have accepted the configuration
		// in the first place; so this is really a problem with the application
		// ("errored") instead of the user's fault ("failed")
		return castellum.OperationOutcomeErrored, err
	}

	// double-check actual `oldSize` by counting current group members
	groupID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	group, err := m.getServerGroup(ctx, groupID)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	oldSize := uint64(len(group.Members))

	// perform server creations/deletions
	if oldSize > newSize {
		return m.terminateServers(ctx, res, cfg, group, oldSize-newSize)
	}
	if newSize > oldSize {
		return m.createServers(ctx, res, cfg, group, newSize-oldSize)
	}

	// nothing to do (should be unreachable in practice since we would not get called at all when `oldSize == newSize`)
	return castellum.OperationOutcomeSucceeded, nil
}

func (m *assetManagerServerGroups) terminateServers(ctx context.Context, res db.Resource, cfg configForServerGroup, group serverGroup, countToDelete uint64) (castellum.OperationOutcome, error) {
	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	provider, eo, err := m.Provider.ProjectScopedClient(ctx, core.ProjectScope{
		ID:        res.ScopeUUID,
		RoleNames: m.LocalRoleNames,
	})
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	loadbalancerV2, err := openstack.NewLoadBalancerV2(provider, eo)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}

	// get creation timestamps for all servers in this group
	var allServers []*servers.Server
	for _, serverID := range group.Members {
		server, err := servers.Get(ctx, computeV2, serverID).Extract()
		if err != nil {
			return castellum.OperationOutcomeErrored, fmt.Errorf("cannot inspect server %s in %s: %w", serverID, res.AssetType, err)
		}
		allServers = append(allServers, server)
	}

	// sort servers such that those that we want to delete are in front
	if cfg.DeleteNewestFirst {
		// The non-default behavior is to terminate the newest servers. This has
		// been requested by customers who prefer to keep their old servers because
		// they're tried and true.
		sort.Slice(allServers, func(i, j int) bool {
			return allServers[i].Created.After(allServers[j].Created)
		})
	} else {
		// The default behavior is to terminate the oldest servers. This enables the
		// user to roll out config changes by updating the resource config, scaling
		// up to make new servers, then scaling down to remove the old servers.
		sort.Slice(allServers, func(i, j int) bool {
			return allServers[i].Created.Before(allServers[j].Created)
		})
	}

	// delete oldest servers
	serversInDeletion := make(map[string]string)
	for idx := 0; uint64(idx) < countToDelete && idx < len(allServers); idx++ {
		server := allServers[idx]
		logg.Info("deleting server %s from %s", server.ID, res.AssetType)
		for _, lb := range cfg.LoadbalancerPoolMemberships {
			err := m.removeServerFromLoadbalancer(ctx, server, lb, loadbalancerV2)
			if err != nil {
				err = fmt.Errorf("cannot remove server %s in %s from LB pool %s: %w", server.ID, res.AssetType, lb.PoolUUID, err)
				return castellum.OperationOutcomeErrored, err
			}
		}
		if len(cfg.LoadbalancerPoolMemberships) > 0 {
			// give some extra time for the server to answer its last outstanding client requests
			time.Sleep(5 * time.Second)
		}
		err := servers.Delete(ctx, computeV2, server.ID).ExtractErr()
		if err != nil {
			return castellum.OperationOutcomeErrored, fmt.Errorf("cannot delete server %s in %s: %w", server.ID, res.AssetType, err)
		}
		serversInDeletion[server.ID] = server.Status
	}

	// wait for servers to be deleted
	start := time.Now()
	for len(serversInDeletion) > 0 {
		time.Sleep(ServerPollInterval)

		// error if servers are still deleting after timeout
		if time.Since(start) > ServerDeletionTimeout {
			var msgs []string
			for serverID, status := range serversInDeletion {
				msgs = append(msgs, fmt.Sprintf("%s is in status %q", serverID, status))
			}
			sort.Strings(msgs)
			return castellum.OperationOutcomeErrored, fmt.Errorf("timeout waiting for server deletion in %s: %s", res.AssetType, strings.Join(msgs, ", "))
		}

		// check if servers are still there
		logg.Info("checking on %d servers being deleted...", len(serversInDeletion))
		for serverID := range serversInDeletion {
			server, err := servers.Get(ctx, computeV2, serverID).Extract()
			if gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
				// server has disappeared - stop waiting for it
				delete(serversInDeletion, serverID)
				continue
			}
			if err != nil {
				return castellum.OperationOutcomeErrored, fmt.Errorf("cannot inspect deleted server %s in %s: %w", serverID, res.AssetType, err)
			}
			// note down changes in server status (we may want to use these for the timeout error message)
			serversInDeletion[server.ID] = server.Status
		}
	}

	return castellum.OperationOutcomeSucceeded, nil
}

func (m *assetManagerServerGroups) createServers(ctx context.Context, res db.Resource, cfg configForServerGroup, group serverGroup, countToCreate uint64) (castellum.OperationOutcome, error) {
	provider, eo, err := m.Provider.ProjectScopedClient(ctx, core.ProjectScope{
		ID:        res.ScopeUUID,
		RoleNames: m.LocalRoleNames,
	})
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	computeV2, err := openstack.NewComputeV2(provider, eo)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	imageV2, err := openstack.NewImageV2(provider, eo)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	keymgrV1, err := openstack.NewKeyManagerV1(provider, eo)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}
	loadbalancerV2, err := openstack.NewLoadBalancerV2(provider, eo)
	if err != nil {
		return castellum.OperationOutcomeErrored, err
	}

	resolvedImageID, err := m.resolveImageIntoID(ctx, imageV2, cfg.Template.Image.Name)
	if err != nil {
		return Classify(err)
	}
	resolvedFlavorID, err := m.resolveFlavorIntoID(ctx, computeV2, cfg.Template.Flavor.Name)
	if err != nil {
		return Classify(err)
	}
	resolvedKeypairName, err := m.pullKeypairFromBarbican(ctx, computeV2, keymgrV1, cfg.Template.PublicKey.BarbicanID)
	if err != nil {
		return Classify(err)
	}

	// build creation request template
	var networkOpts []servers.Network
	for _, net := range cfg.Template.Networks {
		networkOpts = append(networkOpts, servers.Network{UUID: net.UUID, Tag: net.Tag})
	}
	opts := func(name string) (opts servers.CreateOptsBuilder) {
		opts = servers.CreateOpts{
			AvailabilityZone: cfg.Template.AvailabilityZone,
			BlockDevice:      cfg.Template.BlockDeviceMappings,
			FlavorRef:        resolvedFlavorID,
			ImageRef:         resolvedImageID,
			Metadata:         cfg.Template.Metadata,
			Name:             name,
			Networks:         networkOpts,
			SecurityGroups:   cfg.Template.SecurityGroupNames,
			UserData:         cfg.Template.UserData,
		}
		opts = keypairs.CreateOptsExt{
			CreateOptsBuilder: opts,
			KeyName:           resolvedKeypairName,
		}
		return opts
	}
	schedulerhints := servers.SchedulerHintOpts{
		Group: group.ID,
	}

	// create servers
	serversInCreation := make(map[string]string)
	for idx := 0; uint64(idx) < countToCreate; idx++ {
		name := fmt.Sprintf("%s-%s", group.Name, makeNameDisambiguator())
		logg.Info("creating server %s in %s", name, res.AssetType)

		server, err := servers.Create(ctx, computeV2, opts(name), schedulerhints).Extract()
		if err != nil {
			err = fmt.Errorf("cannot create server %s in %s: %w", name, res.AssetType, err)
			if strings.Contains(err.Error(), "Quota exceeded for ") {
				return castellum.OperationOutcomeFailed, err
			} else {
				return castellum.OperationOutcomeErrored, err
			}
		}
		serversInCreation[server.ID] = server.Status
	}

	// wait for servers to get into ACTIVE
	start := time.Now()
	var msgs []string // accumulates all errors during the following loop
	for len(serversInCreation) > 0 {
		time.Sleep(ServerPollInterval)

		// error if servers are still creating after timeout
		if time.Since(start) > ServerCreationTimeout {
			for serverID, status := range serversInCreation {
				msgs = append(msgs, fmt.Sprintf("server %s has not reached status ACTIVE (currently in status %q)", serverID, status))
			}
			break
		}

		// check if servers have progressed
		logg.Info("checking on %d servers being created...", len(serversInCreation))
		for serverID := range serversInCreation {
			server, err := servers.Get(ctx, computeV2, serverID).Extract()
			if gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
				// server has disappeared - complain, and stop checking for it
				msgs = append(msgs, fmt.Sprintf("server %s has disappeared before going into ACTIVE", serverID))
				delete(serversInCreation, serverID)
				continue
			}
			if err != nil {
				// This is not a fatal error. There can always be short API outages and
				// such; that should not make the entire resize fail. Most such errors
				// are caught with a retry logic on the level of ExecuteNextResize(),
				// but this is one instance where we have it inside of SetAssetSize()
				// since it would not helpful to restart the entire SetAssetSize().
				logg.Error("could not check status for created server %s in %s: %s", serverID, res.AssetType, err.Error())
				continue
			}

			switch server.Status {
			case "ACTIVE":
				logg.Info("server %s in %s has entered status ACTIVE", serverID, res.AssetType)
				for _, lb := range cfg.LoadbalancerPoolMemberships {
					err := m.addServerToLoadbalancer(ctx, server, lb, loadbalancerV2)
					if err != nil {
						msgs = append(msgs, fmt.Sprintf("cannot add server %s to LB pool %s: %s", serverID, lb.PoolUUID, err.Error()))
					}
				}
				delete(serversInCreation, serverID)
			case "ERROR":
				msgs = append(msgs, fmt.Sprintf("server %s has entered status ERROR with message %q", serverID, server.Fault.Code))
				delete(serversInCreation, serverID)
			default:
				// keep waiting for this server to get into ACTIVE (or ERROR)
			}
		}
	}

	if len(msgs) == 0 {
		return castellum.OperationOutcomeSucceeded, nil
	}
	sort.Strings(msgs)
	return castellum.OperationOutcomeErrored, fmt.Errorf("timeout waiting for server creation in %s: %s", res.AssetType, strings.Join(msgs, ", "))
}

func makeNameDisambiguator() string {
	// 5 bytes of data encode to exactly 8 base32 characters without padding
	var buf [5]byte
	_ = must.Return(rand.Read(buf[:])) // ignores result value (number of bytes read)
	return base32.StdEncoding.EncodeToString(buf[:])
}

func (m *assetManagerServerGroups) findServerIPForLoadbalancer(server *servers.Server, _ configForLBPoolMembership) (string, error) {
	//TODO: We should probably check that the IP address is from a subnet that
	// the LB can actually reach. For now, I'll just assume that the user will
	// only configure one private network on the auto-created instances, which
	// means that there is no question which IP to choose.
	for _, entry := range server.Addresses {
		addrInfos, ok := entry.([]any)
		if ok {
			for _, info := range addrInfos {
				addrInfo, ok := info.(map[string]any)
				if ok && addrInfo["OS-EXT-IPS:type"] == "fixed" {
					ip, ok := addrInfo["addr"].(string)
					if ok && ip != "" {
						return ip, nil
					}
				}
			}
		}
	}
	return "", errors.New("cannot find IP address for server")
}

func (m *assetManagerServerGroups) addServerToLoadbalancer(ctx context.Context, server *servers.Server, cfg configForLBPoolMembership, loadbalancerV2 *gophercloud.ServiceClient) error {
	serverIP, err := m.findServerIPForLoadbalancer(server, cfg)
	if err != nil {
		return err
	}
	opts := pools.CreateMemberOpts{
		Name:         server.Name,
		Address:      serverIP,
		ProtocolPort: int(cfg.ProtocolPort),
	}
	if cfg.MonitorPort != 0 {
		val := int(cfg.MonitorPort)
		opts.MonitorAddress = serverIP
		opts.MonitorPort = &val
	}
	_, err = pools.CreateMember(ctx, loadbalancerV2, cfg.PoolUUID, opts).Extract()
	return err
}

func (m *assetManagerServerGroups) removeServerFromLoadbalancer(ctx context.Context, server *servers.Server, cfg configForLBPoolMembership, loadbalancerV2 *gophercloud.ServiceClient) error {
	listOpts := pools.ListMembersOpts{
		Name: server.Name,
	}
	pager, err := pools.ListMembers(loadbalancerV2, cfg.PoolUUID, listOpts).AllPages(ctx)
	if err != nil {
		return err
	}
	members, err := pools.ExtractMembers(pager)
	if err != nil {
		return err
	}
	for _, member := range members {
		err := pools.DeleteMember(ctx, loadbalancerV2, cfg.PoolUUID, member.ID).ExtractErr()
		if err != nil {
			return err
		}
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// resource configuration

type configForLBPoolMembership struct {
	PoolUUID     string `json:"pool_uuid"`
	ProtocolPort uint16 `json:"protocol_port"`
	MonitorPort  uint16 `json:"monitor_port"`
}

type configForServerGroup struct {
	DeleteNewestFirst bool `json:"delete_newest_first"`
	Template          struct {
		AvailabilityZone    string                `json:"availability_zone"`
		BlockDeviceMappings []servers.BlockDevice `json:"block_device_mapping_v2,omitempty"`
		Flavor              struct {
			Name string `json:"name"`
		} `json:"flavor"`
		Image struct {
			Name string `json:"name"`
		} `json:"image"`
		Metadata map[string]string `json:"metadata"`
		Networks []struct {
			UUID string `json:"uuid"`
			Tag  string `json:"tag"`
		} `json:"networks"`
		PublicKey struct {
			BarbicanID string `json:"barbican_uuid"`
		} `json:"public_key"`
		SecurityGroupNames []string `json:"security_groups"`
		UserData           []byte   `json:"user_data"`
	} `json:"template"`
	LoadbalancerPoolMemberships []configForLBPoolMembership `json:"loadbalancer_pool_memberships"`
}

var validBDMSourceTypes = []servers.SourceType{
	servers.SourceBlank,
	servers.SourceImage,
	servers.SourceSnapshot,
	servers.SourceVolume,
}

var validBDMDestinationTypes = []servers.DestinationType{
	servers.DestinationLocal,
	servers.DestinationVolume,
}

func joinStrings[S ~string](inputs []S, separator string) string {
	outputs := make([]string, len(inputs))
	for idx, input := range inputs {
		outputs[idx] = string(input)
	}
	return strings.Join(outputs, separator)
}

func (m *assetManagerServerGroups) parseAndValidateConfig(configJSON string) (configForServerGroup, error) {
	var cfg configForServerGroup
	dec := json.NewDecoder(strings.NewReader(configJSON))
	dec.DisallowUnknownFields()
	err := dec.Decode(&cfg)
	if err != nil {
		return configForServerGroup{}, fmt.Errorf("cannot parse configuration: %w", err)
	}

	var errs []string
	complain := func(msg string, args ...any) {
		if len(args) > 0 {
			msg = fmt.Sprintf(msg, args...)
		}
		errs = append(errs, msg)
	}

	for idx, bd := range cfg.Template.BlockDeviceMappings {
		if bd.SourceType == "" {
			complain("template.block_device_mapping_v2[%d].source_type is missing", idx)
		} else if !slices.Contains(validBDMSourceTypes, bd.SourceType) {
			complain("value for template.block_device_mapping_v2[%d].source_type must be one of: %q",
				idx, joinStrings(validBDMSourceTypes, `", "`))
		}
		if bd.DestinationType == "" {
			// this is acceptable apparently
		} else if !slices.Contains(validBDMDestinationTypes, bd.DestinationType) {
			complain("value for template.block_device_mapping_v2[%d].destination_type must be one of: %q",
				idx, joinStrings(validBDMDestinationTypes, `", "`))
		}
	}
	for idx, lb := range cfg.LoadbalancerPoolMemberships {
		if lb.PoolUUID == "" {
			complain("loadbalancer_pool_memberships[%d].pool_uuid is missing", idx)
		}
		if lb.ProtocolPort == 0 {
			complain("loadbalancer_pool_memberships[%d].protocol_port is missing", idx)
		}
	}
	if cfg.Template.Flavor.Name == "" {
		complain("template.flavor.name is missing")
	}
	if cfg.Template.Image.Name == "" {
		complain("template.image.name is missing")
	}
	for k, v := range cfg.Template.Metadata {
		if len(k) > 255 {
			complain("key for template.metadata[%q] is too long (%d bytes, but max is 255 bytes)", k, len(k))
		}
		if len(v) > 255 {
			complain("value for template.metadata[%q] is too long (%d bytes, but max is 255 bytes)", k, len(v))
		}
	}
	if len(cfg.Template.Networks) == 0 {
		complain("template.networks is missing")
	}
	for idx, net := range cfg.Template.Networks {
		if net.UUID == "" {
			complain("template.networks[%d].uuid is missing", idx)
		}
	}
	if cfg.Template.PublicKey.BarbicanID == "" {
		complain("template.public_key.barbican_uuid is missing")
	}
	if len(cfg.Template.SecurityGroupNames) == 0 {
		complain("template.security_groups is missing")
	}
	if len(cfg.Template.UserData) > 65535 {
		complain("template.user_data is too long (%d bytes, but max is 65535 bytes)", len(cfg.Template.UserData))
	}

	if len(errs) > 0 {
		return configForServerGroup{}, fmt.Errorf("configuration is invalid: %s", strings.Join(errs, ", "))
	}
	return cfg, nil
}

////////////////////////////////////////////////////////////////////////////////
// gophercloud extensions/helpers

// serverGroup is like type servergroups.ServerGroup, but contains fields
// that the latter does not yet have.
type serverGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	ProjectID string   `json:"project_id"` // not in Gophercloud (TODO)
}

func (m *assetManagerServerGroups) getServerGroup(ctx context.Context, id string) (serverGroup, error) {
	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return serverGroup{}, err
	}
	computeV2.Microversion = "2.13" // for ProjectID attribute on server group

	var data struct {
		ServerGroup serverGroup `json:"server_group"`
	}
	err = servergroups.Get(ctx, computeV2, id).ExtractInto(&data)
	return data.ServerGroup, err
}

func (m *assetManagerServerGroups) resolveImageIntoID(ctx context.Context, imageV2 *gophercloud.ServiceClient, name string) (string, error) {
	page, err := images.List(imageV2, images.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot get image %q: %w", name, err)
	}
	matchingImages, err := images.ExtractImages(page)
	if err != nil {
		return "", fmt.Errorf("cannot get image %q: %w", name, err)
	}
	if len(matchingImages) == 0 {
		return "", UserError{fmt.Errorf("image not found: %q", name)}
	}
	if len(matchingImages) > 1 {
		return "", UserError{fmt.Errorf("image name is not unique: %q", name)}
	}
	return matchingImages[0].ID, nil
}

func (m *assetManagerServerGroups) resolveFlavorIntoID(ctx context.Context, computeV2 *gophercloud.ServiceClient, name string) (string, error) {
	page, err := flavors.ListDetail(computeV2, flavors.ListOpts{}).AllPages(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot get flavor %q: %w", name, err)
	}
	allFlavors, err := flavors.ExtractFlavors(page)
	if err != nil {
		return "", fmt.Errorf("cannot get flavor %q: %w", name, err)
	}
	var matchingFlavors []flavors.Flavor
	for _, flavor := range allFlavors {
		if flavor.Name == name {
			matchingFlavors = append(matchingFlavors, flavor)
		}
	}
	if len(matchingFlavors) == 0 {
		return "", UserError{fmt.Errorf("flavor not found: %q", name)}
	}
	if len(matchingFlavors) > 1 {
		return "", UserError{fmt.Errorf("flavor name is not unique: %q", name)}
	}
	return matchingFlavors[0].ID, nil
}

func (m *assetManagerServerGroups) pullKeypairFromBarbican(ctx context.Context, computeV2, keymgrV1 *gophercloud.ServiceClient, secretID string) (string, error) {
	// check if present in Nova already
	nameInNova := "from-barbican-" + secretID
	_, err := keypairs.Get(ctx, computeV2, nameInNova, nil).Extract()
	switch {
	case err == nil:
		// keypair exists -> nothing to do
		return nameInNova, nil
	case gophercloud.ResponseCodeIs(err, http.StatusNotFound):
		// keypair does not exist -> pull from Barbican below
		break
	default:
		// unexpected error
		return "", nil
	}

	// keypair does not exist -> pull from Barbican
	payload, err := secrets.GetPayload(ctx, keymgrV1, secretID, nil).Extract()
	if err != nil {
		// This is not guaranteed to be a UserError, but the most common errors are
		// going to be 401 Forbidden (the secret was created as private and cannot
		// be read by our service user) and 404 Not Found (the wrong ID was given or
		// the secret was deleted in the meantime).
		return "", UserError{fmt.Errorf("cannot get public key from Barbican: %w", err)}
	}
	_, err = keypairs.Create(ctx, computeV2, keypairs.CreateOpts{Name: nameInNova, PublicKey: string(payload)}).Extract()
	if err != nil {
		return "", fmt.Errorf("cannot upload public key to Nova: %w", err)
	}
	return nameInNova, nil
}
