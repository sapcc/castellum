# Asset manager: `project-quota`

The asset manager `project-quota` provides one asset type for each quota in [Limes](https://github.com/sapcc/limes), e.g.,

```
project-quota:compute:cores
project-quota:compute:instances
project-quota:compute:ram
project-quota:network:floating_ips
etc.
```

Each such project resource contains exactly one asset, the project itself. The asset UUID is the project ID.

## Required permissions

The Castellum service user must be able to:

- retrieve information about any project from Keystone (to find the domain ID for a project), and
- get/set any project quotas in Limes.

## Policy considerations

- `project:show:project-quota` can be given to everyone who has read access to Limes quotas in the project.
- `project:edit:project-quota` should only be given to users who can set project quota, i.e. usually only to domain
  admins or cluster admins.
