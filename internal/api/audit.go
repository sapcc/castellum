// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/sapcc/go-api-declarations/cadf"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/must"
)

// EventParams contains parameters for creating an audit event.
type scalingEventTarget struct {
	projectID string
	resource  *castellum.Resource // only used for enable/update action events
}

func (t scalingEventTarget) Render() cadf.Resource {
	result := cadf.Resource{
		TypeURI:   "data/security/project",
		ID:        t.projectID,
		ProjectID: t.projectID,
	}
	if t.resource != nil {
		attachment := must.Return(cadf.NewJSONAttachment("payload", *t.resource))
		result.Attachments = append(result.Attachments, attachment)
	}
	return result
}
