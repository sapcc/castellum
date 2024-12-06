/******************************************************************************
*
*  Copyright 2019 SAP SE
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
