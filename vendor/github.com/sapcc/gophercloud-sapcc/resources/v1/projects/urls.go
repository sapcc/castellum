package projects

import "github.com/gophercloud/gophercloud"

func listURL(client *gophercloud.ServiceClient, domainID string) string {
	return client.ServiceURL("domains", domainID, "projects")
}

func getURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID)
}

func updateURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID)
}

func syncURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID, "sync")
}
