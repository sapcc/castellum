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
	"encoding/json"

	"github.com/sapcc/go-api-declarations/cadf"
	"github.com/sapcc/go-api-declarations/castellum"
)

// EventParams contains parameters for creating an audit event.
type scalingEventTarget struct {
	projectID         string
	attachmentContent targetAttachmentContent // only used for enable/update action events
}

func (t scalingEventTarget) Render() cadf.Resource {
	return cadf.Resource{
		TypeURI:   "data/security/project",
		ID:        t.projectID,
		ProjectID: t.projectID,
		Attachments: []cadf.Attachment{{
			Name:    "payload",
			TypeURI: "mime:application/json",
			Content: t.attachmentContent,
		}},
	}
}

type targetAttachmentContent struct {
	resource castellum.Resource
}

// MarshalJSON implements the json.Marshaler interface.
func (a targetAttachmentContent) MarshalJSON() ([]byte, error) {
	// copy resource data into a struct that does not have a custom MarshalJSON
	data := a.resource

	// Hermes does not accept a JSON object at target.attachments[].content, so
	// we need to wrap the marshaled JSON into a JSON string
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return json.Marshal(string(bytes))
}
