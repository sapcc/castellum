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
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/hermes/pkg/cadf"
	"github.com/sapcc/hermes/pkg/rabbit"
	"github.com/streadway/amqp"
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

	s := make(chan cadf.Event, 20)
	eventSink = s
	go commitAuditTrail(s)
}

//commitAuditTrail receives the audit events from an event sink channel
//and sends them to a RabbitMQ server.
func commitAuditTrail(eventSink <-chan cadf.Event) {
	rc := &rabbitConnection{}
	connect := func() {
		if !rc.isConnected {
			err := rc.connect(rabbitURI, rabbitQueueName)
			if err != nil {
				logg.Error(err.Error())
			}
		}
	}
	sendEvent := func(e *cadf.Event) bool {
		if !rc.isConnected {
			return false
		}
		err := rabbit.PublishEvent(rc.ch, rc.q.Name, e)
		if err != nil {
			eventPublishFailedCounter.Inc()
			logg.Error("RabbitMQ: failed to publish audit event with ID %q: %s", e.ID, err.Error())
			return false
		}
		eventPublishSuccessCounter.Inc()
		return true
	}

	var pendingEvents []cadf.Event

	ticker := time.Tick(1 * time.Minute)
	for {
		select {
		case e := <-eventSink:
			if showAuditOnStdout {
				msg, _ := json.Marshal(e)
				logg.Other("AUDIT", string(msg))
			}
			if sendAuditToRabbitMQ {
				connect()
				if successful := sendEvent(&e); !successful {
					pendingEvents = append(pendingEvents, e)
				}
			}
		case <-ticker:
			if sendAuditToRabbitMQ {
				for len(pendingEvents) > 0 {
					connect()
					successful := false //until proven otherwise
					nextEvent := pendingEvents[0]
					if successful = sendEvent(&nextEvent); !successful {
						//refresh connection, if old
						if time.Since(rc.connectedAt) > (5 * time.Minute) {
							rc.disconnect()
							connect()
						}
						time.Sleep(5 * time.Second)
						successful = sendEvent(&nextEvent) //one more try before giving up
					}

					if successful {
						pendingEvents = pendingEvents[1:]
					} else {
						break
					}
				}
			}
		}
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

///////////////////////////////////////////////////////////////////////////////
//Helper functions

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

//rabbitConnection represents a unique connection to some RabbitMQ server with
//an open Channel and a declared Queue.
type rabbitConnection struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	q    amqp.Queue

	isConnected bool
	connectedAt time.Time
}

func (r *rabbitConnection) connect(uri, queueName string) error {
	var err error

	//establish a connection with the RabbitMQ server
	r.conn, err = amqp.Dial(uri)
	if err != nil {
		return fmt.Errorf("RabbitMQ: failed to establish a connection with the server: %s", err.Error())
	}
	r.connectedAt = time.Now()

	//open a unique, concurrent server channel to process the bulk of AMQP messages
	r.ch, err = r.conn.Channel()
	if err != nil {
		return fmt.Errorf("RabbitMQ: failed to open a channel: %s", err.Error())
	}

	//declare a queue to hold and deliver messages to consumers
	r.q, err = rabbit.DeclareQueue(r.ch, queueName)
	if err != nil {
		return fmt.Errorf("RabbitMQ: failed to declare a queue: %s", err.Error())
	}

	r.isConnected = true

	return nil
}

func (r *rabbitConnection) disconnect() {
	r.ch.Close()
	r.conn.Close()
	r.isConnected = false
}
