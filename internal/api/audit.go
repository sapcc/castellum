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
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/hermes/pkg/cadf"
	"github.com/streadway/amqp"
)

//eventSink is a channel that receives audit events.
var eventSink chan<- cadf.Event

var showAuditOnStdout bool

// StartAuditLogging starts audit logging for the API.
func StartAuditLogging(rabbitQueueName string, rabbitURI amqp.URI) {
	silenceAuditLogging, _ := strconv.ParseBool(os.Getenv("CASTELLUM_AUDIT_SILENT"))
	showAuditOnStdout = !silenceAuditLogging

	auditEventPublishSuccessCounter.Add(0)
	auditEventPublishFailedCounter.Add(0)

	onSuccessFunc := func() {
		auditEventPublishSuccessCounter.Inc()
	}
	onFailFunc := func() {
		auditEventPublishFailedCounter.Inc()
	}
	s := make(chan cadf.Event, 20)
	eventSink = s

	go audittools.AuditTrail{
		EventSink:           s,
		OnSuccessfulPublish: onSuccessFunc,
		OnFailedPublish:     onFailFunc,
	}.Commit(rabbitQueueName, rabbitURI)
}

var observerUUID = audittools.GenerateUUID()

//logAndPublishEvent logs the audit event to stdout and publishes it to a RabbitMQ server.
func logAndPublishEvent(time time.Time, req *http.Request, token *gopherpolicy.Token, reasonCode int, target audittools.TargetRenderer) {
	action := "update"
	if v, ok := target.(scalingEventTarget); ok {
		action = string(v.action) + "/" + v.resourceType
	}
	p := audittools.EventParameters{
		Time:       time,
		Request:    req,
		User:       token,
		ReasonCode: reasonCode,
		Action:     action,
		Observer: struct {
			TypeURI string
			Name    string
			ID      string
		}{
			TypeURI: "service/autoscaling",
			Name:    "castellum",
			ID:      observerUUID,
		},
		Target: target,
	}
	event := audittools.NewEvent(p)

	if showAuditOnStdout {
		msg, _ := json.Marshal(event)
		logg.Other("AUDIT", string(msg))
	}

	if eventSink != nil {
		eventSink <- event
	}
}

type auditAction string

//Different type of actions that are used to create the appropriate value for
//cadf.Event.Action.
const (
	updateAction  auditAction = "update"
	enableAction  auditAction = "enable"
	disableAction auditAction = "disable"
)

//EventParams contains parameters for creating an audit event.
type scalingEventTarget struct {
	action            auditAction
	projectID         string
	resourceType      string
	attachmentContent targetAttachmentContent //only used for enable/update action events
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
	resource Resource
}

//MarshalJSON implements the json.Marshaler interface.
func (a targetAttachmentContent) MarshalJSON() ([]byte, error) {
	//copy resource data into a struct that does not have a custom MarshalJSON
	data := a.resource

	//Hermes does not accept a JSON object at target.attachments[].content, so
	//we need to wrap the marshaled JSON into a JSON string
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return json.Marshal(string(bytes))
}
