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
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/hermes/pkg/cadf"
)

var eventPublishSuccessCounter = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "castellum_successful_auditevent_publish",
		Help: "Counter for successful audit event publish to RabbitMQ server.",
	},
)
var eventPublishFailedCounter = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "castellum_failed_auditevent_publish",
		Help: "Counter for failed audit event publish to RabbitMQ server.",
	},
)

//eventSink is a channel that receives audit events.
var eventSink chan<- cadf.Event

var (
	showAuditOnStdout   bool
	sendAuditToRabbitMQ bool
	rabbitURI           string
	rabbitQueueName     string
	observerUUID        = generateUUID()
)

func init() {
	prometheus.MustRegister(eventPublishSuccessCounter)
	prometheus.MustRegister(eventPublishFailedCounter)

	silenceAuditLogging, _ := strconv.ParseBool(os.Getenv("CASTELLUM_AUDIT_SILENT"))
	showAuditOnStdout = !silenceAuditLogging

	rabbitURI = os.Getenv("CASTELLUM_RABBITMQ_URI")
	if rabbitURI != "" {
		rabbitQueueName = os.Getenv("CASTELLUM_RABBITMQ_QUEUE_NAME")
		if rabbitQueueName == "" {
			logg.Fatal("missing required environment variable: CASTELLUM_RABBITMQ_QUEUE_NAME")
		}
		sendAuditToRabbitMQ = true
	}

	if sendAuditToRabbitMQ {
		eventPublishSuccessCounter.Add(0)
		eventPublishFailedCounter.Add(0)

		onSuccessFunc := func() {
			eventPublishSuccessCounter.Inc()
		}
		onFailFunc := func() {
			eventPublishFailedCounter.Inc()
		}
		s := make(chan cadf.Event, 20)
		eventSink = s

		go audittools.AuditTrail{
			EventSink:           s,
			OnSuccessfulPublish: onSuccessFunc,
			OnFailedPublish:     onFailFunc,
		}.Commit(rabbitURI, rabbitQueueName)
	}
}

//logAndPublishEvent logs the audit event to stdout and publishes it to a RabbitMQ server.
func logAndPublishEvent(event cadf.Event) {
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
type auditEventParams struct {
	token             *gopherpolicy.Token
	request           *http.Request
	time              time.Time
	reasonCode        int
	action            auditAction
	projectID         string
	resourceType      string
	attachmentContent attachmentContent //only used for enable/update action events
}

type attachmentContent struct {
	resource Resource
}

//MarshalJSON implements the json.Marshaler interface.
func (a attachmentContent) MarshalJSON() ([]byte, error) {
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

//newAuditEvent takes the necessary parameters and returns a new audit event.
func newAuditEvent(p auditEventParams) cadf.Event {
	outcome := "failure"
	if p.reasonCode >= 200 && p.reasonCode < 300 {
		outcome = "success"
	}

	action := string(p.action) + "/" + p.resourceType

	return cadf.Event{
		TypeURI:   "http://schemas.dmtf.org/cloud/audit/1.0/event",
		ID:        generateUUID(),
		EventTime: p.time.Format("2006-01-02T15:04:05.999999+00:00"),
		EventType: "activity",
		Action:    action,
		Outcome:   outcome,
		Reason: cadf.Reason{
			ReasonType: "HTTP",
			ReasonCode: strconv.Itoa(p.reasonCode),
		},
		Initiator: cadf.Resource{
			TypeURI:   "service/security/account/user",
			Name:      p.token.Context.Auth["user_name"],
			ID:        p.token.Context.Auth["user_id"],
			Domain:    p.token.Context.Auth["domain_name"],
			DomainID:  p.token.Context.Auth["domain_id"],
			ProjectID: p.token.Context.Auth["project_id"],
			Host: &cadf.Host{
				Address: tryStripPort(p.request.RemoteAddr),
				Agent:   p.request.Header.Get("User-Agent"),
			},
		},
		Target: cadf.Resource{
			TypeURI:   "data/security/project",
			ID:        p.projectID,
			ProjectID: p.projectID,
			Attachments: []cadf.Attachment{{
				Name:    "payload",
				TypeURI: "mime:application/json",
				Content: p.attachmentContent,
			}},
		},
		Observer: cadf.Resource{
			TypeURI: "service/autoscaling",
			Name:    "castellum",
			ID:      observerUUID,
		},
		RequestPath: p.request.URL.String(),
	}
}

func generateUUID() string {
	u, err := uuid.NewV4()
	if err != nil {
		logg.Fatal(err.Error())
	}

	return u.String()
}

func tryStripPort(hostPort string) string {
	host, _, err := net.SplitHostPort(hostPort)
	if err == nil {
		return host
	}
	return hostPort
}
