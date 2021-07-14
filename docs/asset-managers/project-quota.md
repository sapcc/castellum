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

Project resources will only be allowed when the corresponding project resource in Limes has the annotation
`can_autoscale` with a value of `true`. Refer to [this section of the Limes documentation][limes-doc] for how to add
those annotations.

[limes-doc]: https://github.com/sapcc/limes/blob/master/docs/operators/config.md#resource-behavior

## User considerations

### Resource configuration

The Castellum API does not accept any additional configuration for `project-quota:*` resources.

## Operational considerations

### Required permissions

The Castellum service user must be able to:

- retrieve information about any project from Keystone (to find the domain ID for a project), and
- get/set any project quotas in Limes.

### Policy considerations

- `project:show:project-quota` can be given to everyone who has read access to Limes quotas in the project.
- `project:edit:project-quota` should only be given to users who can set project quota, i.e. usually only to domain
  admins or cluster admins.
