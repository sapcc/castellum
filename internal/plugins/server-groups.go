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
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/schedulerhints"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/images"
	"github.com/gophercloud/gophercloud/openstack/keymanager/v1/secrets"
	prom_api "github.com/prometheus/client_golang/api"
	prom_v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
)

const (
	//ServerDeletionTimeout is how long the "server-groups" asset manager will
	//wait for servers to be deleted before reporting an error.
	ServerDeletionTimeout = 3 * time.Minute
	//ServerCreationTimeout is how long the "server-groups" asset manager will
	//wait for new servers to go into state ACTIVE before reporting an error.
	ServerCreationTimeout = 5 * time.Minute
	//ServerPollInterval is how often servers are polled during state transitions
	//when the desired state has not been reached yet.
	ServerPollInterval = 10 * time.Second
)

//NOTE 1: The `virtualmachine` labels look like `$NAME ($ID)` or just `$ID`, the
//latter without parentheses around the ID.
//
//NOTE 2: These queries return fractional values in the range 0..1, NOT percentages in the range 0..100.
var serverUsageQueries = map[db.UsageMetric]string{
	"cpu": `vrops_virtualmachine_cpu_usage_ratio{virtualmachine=~".*${ID}.*"} / 100`,
	"ram": `vrops_virtualmachine_memory_consumed_kilobytes{virtualmachine=~".*${ID}.*"} / vrops_virtualmachine_memory_kilobytes{virtualmachine=~".*${ID}.*"}`,
}

type assetManagerServerGroups struct {
	Provider       core.ProviderClient
	Prometheus     prom_v1.API
	LocalRoleNames []string
}

func init() {
	core.RegisterAssetManagerFactory("server-groups", func(provider core.ProviderClient) (core.AssetManager, error) {
		prometheusURL := os.Getenv("CASTELLUM_SERVERGROUPS_PROMETHEUS_URL")
		if prometheusURL == "" {
			return nil, errors.New("missing required environment variable: CASTELLUM_SERVERGROUPS_PROMETHEUS_URL")
		}
		promClient, err := prom_api.NewClient(prom_api.Config{Address: prometheusURL})
		if err != nil {
			return nil, fmt.Errorf("cannot connect to Prometheus at %s: %s",
				prometheusURL, err.Error())
		}

		localRoleNamesStr := os.Getenv("CASTELLUM_SERVERGROUPS_LOCAL_ROLES")
		if localRoleNamesStr == "" {
			return nil, errors.New("missing required environment variable: CASTELLUM_SERVERGROUPS_LOCAL_ROLES")
		}
		var localRoleNames []string
		for _, roleName := range strings.Split(localRoleNamesStr, ",") {
			roleName = strings.TrimSpace(roleName)
			if roleName != "" {
				localRoleNames = append(localRoleNames, roleName)
			}
		}

		return &assetManagerServerGroups{provider, prom_v1.NewAPI(promClient), localRoleNames}, nil
	})
}

//InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerServerGroups) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if strings.HasPrefix(string(assetType), "server-group:") {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []db.UsageMetric{"cpu", "ram"},
		}
	}
	return nil
}

//CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerServerGroups) CheckResourceAllowed(assetType db.AssetType, scopeUUID string, configJSON string) error {
	//check that the server group exists and is in the right project
	groupID := strings.TrimPrefix(string(assetType), "server-group:")
	group, err := m.getServerGroup(groupID)
	if _, is404 := err.(gophercloud.ErrDefault404); is404 || (err == nil && group.ProjectID != scopeUUID) {
		return fmt.Errorf("server group not found in Nova: %s", groupID)
	}
	if err != nil {
		return err
	}

	//check that the config is valid
	_, err = m.parseAndValidateConfig(configJSON)
	return err
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerServerGroups) ListAssets(res db.Resource) ([]string, error) {
	groupUUID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	return []string{groupUUID}, nil
}

//GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerServerGroups) GetAssetStatus(res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return core.AssetStatus{}, err
	}

	groupID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	group, err := m.getServerGroup(groupID)
	if err != nil {
		return core.AssetStatus{}, fmt.Errorf("cannot GET server group: %w", err)
	}

	//check instance status
	isNewServer := make(map[string]bool)
	for _, serverID := range group.Members {
		server, err := servers.Get(computeV2, serverID).Extract()
		if err != nil {
			return core.AssetStatus{}, fmt.Errorf("cannot inspect server %s: %w", serverID, err)
		}
		//if any instance is not in a running state, that's a huge red flag and we
		//should not attempt any autoscaling until all servers are back into a
		//running state
		if server.Status == "ERROR" || server.Status == "SHUTOFF" {
			return core.AssetStatus{}, fmt.Errorf("server %s is in status %s", serverID, server.Status)
		}
		//for new servers, we will be more lenient wrt metric availability
		if time.Since(server.Created) < 10*time.Minute {
			isNewServer[server.ID] = true
		}
	}

	//get usage values for all servers
	result := core.AssetStatus{
		Size:  uint64(len(group.Members)),
		Usage: make(db.UsageValues),
	}
	for metric := range serverUsageQueries {
		result.Usage[metric] = 0
	}
	for _, serverID := range group.Members {
		for metric, queryTemplate := range serverUsageQueries {
			queryStr := strings.Replace(queryTemplate, "${ID}", serverID, -1)
			value, err := prometheusGetSingleValue(m.Prometheus, queryStr)
			if err != nil {
				if _, ok := err.(emptyPrometheusResultErr); ok && isNewServer[serverID] {
					//within a few minutes of instance creation, it's not a hard error if
					//the vrops metric has not showed up in Prometheus yet; we'll just
					//assume zero usage for now, which should be okay since downscaling
					//usually has a delay of way more than those few minutes
					value = 0
				} else {
					return core.AssetStatus{}, err
				}
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

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerServerGroups) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (db.OperationOutcome, error) {
	cfg, err := m.parseAndValidateConfig(res.ConfigJSON)
	if err != nil {
		//if validation fails here, we should not have accepted the configuration
		//in the first place; so this is really a problem with the application
		//("errored") instead of the user's fault ("failed")
		return db.OperationOutcomeErrored, err
	}

	//double-check actual `oldSize` by counting current group members
	groupID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	group, err := m.getServerGroup(groupID)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}
	oldSize = uint64(len(group.Members))

	//perform server creations/deletions
	if oldSize > newSize {
		return m.terminateServers(res, cfg, group, oldSize-newSize)
	}
	if newSize > oldSize {
		return m.createServers(res, cfg, group, newSize-oldSize)
	}

	//nothing to do (should be unreachable in practice since we would not get called at all when `oldSize == newSize`)
	return db.OperationOutcomeSucceeded, nil
}

func (m *assetManagerServerGroups) terminateServers(res db.Resource, cfg configForServerGroup, group serverGroup, countToDelete uint64) (db.OperationOutcome, error) {
	//NOTE: We always terminate the oldest servers. This enables the user to roll
	//out config changes by updating the resource config, scaling up to make new
	//servers, then scaling down to remove the old servers.

	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}

	//get creation timestamps for all servers in this group
	var allServers []*servers.Server
	for _, serverID := range group.Members {
		server, err := servers.Get(computeV2, serverID).Extract()
		if err != nil {
			return db.OperationOutcomeErrored, fmt.Errorf("cannot inspect server %s in %s: %w", serverID, res.AssetType, err)
		}
		allServers = append(allServers, server)
	}
	sort.Slice(allServers, func(i, j int) bool {
		return allServers[i].Created.Before(allServers[j].Created)
	})

	//delete oldest servers
	serversInDeletion := make(map[string]string)
	for idx := 0; uint64(idx) < countToDelete && idx < len(allServers); idx++ {
		server := allServers[idx]
		logg.Info("deleting server %s from %s", server.ID, res.AssetType)
		err := servers.Delete(computeV2, server.ID).ExtractErr()
		if err != nil {
			return db.OperationOutcomeErrored, fmt.Errorf("cannot delete server %s in %s: %w", server.ID, res.AssetType, err)
		}
		serversInDeletion[server.ID] = server.Status
	}

	//wait for servers to be deleted
	start := time.Now()
	for len(serversInDeletion) > 0 {
		time.Sleep(ServerPollInterval)

		//error if servers are still deleting after timeout
		if time.Since(start) > ServerDeletionTimeout {
			var msgs []string
			for serverID, status := range serversInDeletion {
				msgs = append(msgs, fmt.Sprintf("%s is in status %q", serverID, status))
			}
			sort.Strings(msgs)
			return db.OperationOutcomeErrored, fmt.Errorf("timeout waiting for server deletion in %s: %s", res.AssetType, strings.Join(msgs, ", "))
		}

		//check if servers are still there
		logg.Info("checking on %d servers being deleted...", len(serversInDeletion))
		for serverID := range serversInDeletion {
			server, err := servers.Get(computeV2, serverID).Extract()
			if _, ok := err.(gophercloud.ErrDefault404); ok {
				//server has disappeared - stop waiting for it
				delete(serversInDeletion, serverID)
				continue
			}
			if err != nil {
				return db.OperationOutcomeErrored, fmt.Errorf("cannot inspect deleted server %s in %s: %w", serverID, res.AssetType, err)
			}
			//note down changes in server status (we may want to use these for the timeout error message)
			serversInDeletion[server.ID] = server.Status
		}
	}

	return db.OperationOutcomeSucceeded, nil
}

func (m *assetManagerServerGroups) createServers(res db.Resource, cfg configForServerGroup, group serverGroup, countToCreate uint64) (db.OperationOutcome, error) {
	provider, eo, err := m.Provider.ProjectScopedClient(core.ProjectScope{
		ID:        res.ScopeUUID,
		RoleNames: m.LocalRoleNames,
	})
	if err != nil {
		return db.OperationOutcomeErrored, err
	}
	computeV2, err := openstack.NewComputeV2(provider, eo)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}
	imageV2, err := openstack.NewImageServiceV2(provider, eo)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}
	keymgrV1, err := openstack.NewKeyManagerV1(provider, eo)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}

	resolvedImageID, err := m.resolveImageIntoID(imageV2, cfg.Template.Image.Name)
	if err != nil {
		return Classify(err)
	}
	resolvedFlavorID, err := m.resolveFlavorIntoID(computeV2, cfg.Template.Flavor.Name)
	if err != nil {
		return Classify(err)
	}
	resolvedKeypairName, err := m.pullKeypairFromBarbican(computeV2, keymgrV1, cfg.Template.PublicKey.BarbicanID)
	if err != nil {
		return Classify(err)
	}

	//build creation request template
	var networkOpts []servers.Network
	for _, net := range cfg.Template.Networks {
		if net.Tag != "" {
			return db.OperationOutcomeErrored, errors.New("network tags are not supported in Gophercloud") //TODO
		}
		networkOpts = append(networkOpts, servers.Network{UUID: net.UUID})
	}
	opts := func(name string) servers.CreateOptsBuilder {
		opts1 := servers.CreateOpts{
			AvailabilityZone: cfg.Template.AvailabilityZone,
			FlavorRef:        resolvedFlavorID,
			ImageRef:         resolvedImageID,
			Metadata:         cfg.Template.Metadata,
			Name:             name,
			Networks:         networkOpts,
			SecurityGroups:   cfg.Template.SecurityGroupNames,
			UserData:         cfg.Template.UserData,
		}
		opts2 := keypairs.CreateOptsExt{
			CreateOptsBuilder: opts1,
			KeyName:           resolvedKeypairName,
		}
		return schedulerhints.CreateOptsExt{
			CreateOptsBuilder: opts2,
			SchedulerHints: schedulerhints.SchedulerHints{
				Group: group.ID,
			},
		}
	}

	//create servers
	serversInCreation := make(map[string]string)
	for idx := 0; uint64(idx) < countToCreate; idx++ {
		name := fmt.Sprintf("%s-%s", group.Name, makeNameDisambiguator())
		logg.Info("creating server %s in %s", name, res.AssetType)

		server, err := servers.Create(computeV2, opts(name)).Extract()
		if err != nil {
			return db.OperationOutcomeErrored, fmt.Errorf("cannot create server %s in %s: %w", name, res.AssetType, err)
		}
		serversInCreation[server.ID] = server.Status
	}

	//wait for servers to get into ACTIVE, terminate if status ERROR
	start := time.Now()
	var msgs []string //accumulates all errors during the following loop
	for len(serversInCreation) > 0 {
		time.Sleep(ServerPollInterval)

		//error if servers are still creating after timeout
		if time.Since(start) > ServerCreationTimeout {
			for serverID, status := range serversInCreation {
				msgs = append(msgs, fmt.Sprintf("server %s has not reached status ACTIVE (currently in status %q)", serverID, status))
			}
			break
		}

		//check if servers have progressed
		logg.Info("checking on %d servers being created...", len(serversInCreation))
		for serverID := range serversInCreation {
			server, err := servers.Get(computeV2, serverID).Extract()
			if _, ok := err.(gophercloud.ErrDefault404); ok {
				//server has disappeared - complain, and stop checking for it
				msgs = append(msgs, fmt.Sprintf("server %s has disappeared before going into ACTIVE", serverID))
				delete(serversInCreation, serverID)
				continue
			}
			if err != nil {
				//This is not a fatal error. There can always be short API outages and
				//such; that should not make the entire resize fail. Most such errors
				//are caught with a retry logic on the level of ExecuteNextResize(),
				//but this is one instance where we have it inside of SetAssetSize()
				//since it would not helpful to restart the entire SetAssetSize().
				logg.Error("could not check status for created server %s in %s: %s", serverID, res.AssetType, err.Error())
				continue
			}

			switch server.Status {
			case "ACTIVE":
				logg.Info("server %s in %s has entered status ACTIVE", serverID, res.AssetType)
				delete(serversInCreation, serverID)
			case "ERROR":
				msgs = append(msgs, fmt.Sprintf("server %s has entered status ERROR with message %q", serverID, server.Fault.Code))
				delete(serversInCreation, serverID)
			default:
				//keep waiting for this server to get into ACTIVE (or ERROR)
			}
		}
	}

	if len(msgs) == 0 {
		return db.OperationOutcomeSucceeded, nil
	}
	sort.Strings(msgs)
	return db.OperationOutcomeErrored, fmt.Errorf("timeout waiting for server creation in %s: %s", res.AssetType, strings.Join(msgs, ", "))
}

func makeNameDisambiguator() string {
	//5 bytes of data encode to exactly 8 base32 characters without padding
	var buf [5]byte
	_, err := rand.Read(buf[:])
	if err != nil {
		//reading from /dev/urandom should never fail
		logg.Fatal(err.Error())
	}
	return base32.StdEncoding.EncodeToString(buf[:])
}

////////////////////////////////////////////////////////////////////////////////
// resource configuration

type configForServerGroup struct {
	Template struct {
		AvailabilityZone string `json:"availability_zone"`
		Flavor           struct {
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
	complain := func(msg string, args ...interface{}) {
		if len(args) > 0 {
			msg = fmt.Sprintf(msg, args...)
		}
		errs = append(errs, msg)
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

//serverGroup is like type servergroups.ServerGroup, but contains fields
//that the latter does not yet have.
type serverGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	ProjectID string   `json:"project_id"` //not in Gophercloud (TODO)
}

func (m *assetManagerServerGroups) getServerGroup(id string) (serverGroup, error) {
	computeV2, err := m.Provider.CloudAdminClient(openstack.NewComputeV2)
	if err != nil {
		return serverGroup{}, err
	}
	computeV2.Microversion = "2.13" //for ProjectID attribute on server group

	var data struct {
		ServerGroup serverGroup `json:"server_group"`
	}
	err = servergroups.Get(computeV2, id).ExtractInto(&data)
	return data.ServerGroup, err
}

func (m *assetManagerServerGroups) resolveImageIntoID(imageV2 *gophercloud.ServiceClient, name string) (string, error) {
	page, err := images.List(imageV2, images.ListOpts{Name: name}).AllPages()
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

func (m *assetManagerServerGroups) resolveFlavorIntoID(computeV2 *gophercloud.ServiceClient, name string) (string, error) {
	page, err := flavors.ListDetail(computeV2, flavors.ListOpts{}).AllPages()
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

func (m *assetManagerServerGroups) pullKeypairFromBarbican(computeV2, keymgrV1 *gophercloud.ServiceClient, secretID string) (string, error) {
	//check if present in Nova already
	nameInNova := fmt.Sprintf("from-barbican-%s", secretID)
	_, err := keypairs.Get(computeV2, nameInNova).Extract()
	switch err.(type) {
	case nil:
		//keypair exists -> nothing to do
		return nameInNova, nil
	case gophercloud.ErrDefault404:
		//keypair does not exist -> pull from Barbican below
		break
	default:
		//unexpected error
		return "", nil
	}

	//keypair does not exist -> pull from Barbican
	payload, err := secrets.GetPayload(keymgrV1, secretID, nil).Extract()
	if err != nil {
		//This is not guaranteed to be a UserError, but the most common errors are
		//going to be 401 Forbidden (the secret was created as private and cannot
		//be read by our service user) and 404 Not Found (the wrong ID was given or
		//the secret was deleted in the meantime).
		return "", UserError{fmt.Errorf("cannot get public key from Barbican: %w", err)}
	}
	_, err = keypairs.Create(computeV2, keypairs.CreateOpts{Name: nameInNova, PublicKey: string(payload)}).Extract()
	if err != nil {
		return "", fmt.Errorf("cannot upload public key to Nova: %w", err)
	}
	return nameInNova, nil
}
