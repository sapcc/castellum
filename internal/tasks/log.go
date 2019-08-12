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

package tasks

import (
	"fmt"
	"os"

	"github.com/getsentry/sentry-go"
	"github.com/sapcc/go-bits/logg"
)

//sendEventsToSentry tells whether events should be sent to a Sentry server.
var sendEventsToSentry bool

func init() {
	dsn := os.Getenv("CASTELLUM_SENTRY_DSN")
	if dsn != "" {
		err := sentry.Init(sentry.ClientOptions{Dsn: dsn})
		if err != nil {
			logg.Error("Sentry initialization failed: %s", err.Error())
		} else {
			sendEventsToSentry = true
		}
	}
}

//sentryException is the interface that different error types must implement
//in order to generate custom context information for a Sentry event.
type sentryException interface {
	generateTags() map[string]string
	Error() string
}

//captureSentryException is a convenient wrapper around sentry.CaptureException().
//It takes a local *sentry.Hub and generates the relevant tags for the
//custom error type before sending the event to a Sentry server.
//
//Events are grouped by asset_uuid, if that tag is available.
func captureSentryException(hub *sentry.Hub, se sentryException) {
	hub.WithScope(func(scope *sentry.Scope) {
		t := se.generateTags()
		if _, ok := t["asset_uuid"]; ok {
			scope.SetFingerprint([]string{t["asset_uuid"]})
		}
		scope.SetTags(t)
		hub.CaptureException(se)
	})
}

//listAssetsError contains parameters for creating the respective Sentry event.
type listAssetsError struct {
	scopeUUID string
	assetType string
	inner     error
}

//Error implements the tasks.sentryException interface.
func (e listAssetsError) Error() string {
	return fmt.Sprintf("cannot list %s assets in project %s: %s", e.assetType, e.scopeUUID, e.inner.Error())
}

//generateTags implements the tasks.sentryException interface.
func (e listAssetsError) generateTags() map[string]string {
	return map[string]string{
		"scope_uuid": e.scopeUUID,
		"asset_type": e.assetType,
	}
}

//getAssetStatusError contains parameters for creating the respective Sentry event.
type getAssetStatusError struct {
	scopeUUID string
	assetType string
	assetUUID string
	inner     error
}

//Error implements the tasks.sentryException interface.
func (e getAssetStatusError) Error() string {
	return fmt.Sprintf("cannot query status of %s %s: %s", e.assetType, e.assetUUID, e.inner.Error())
}

//generateTags implements the tasks.sentryException interface.
func (e getAssetStatusError) generateTags() map[string]string {
	return map[string]string{
		"scope_uuid": e.scopeUUID,
		"asset_type": e.assetType,
		"asset_uuid": e.assetUUID,
	}
}

//setAssetSizeError contains parameters for creating the respective Sentry event.
type setAssetSizeError struct {
	scopeUUID string
	assetType string
	assetUUID string
	newSize   uint64
	inner     error
}

//generateTags implements the tasks.sentryException interface.
func (e setAssetSizeError) generateTags() map[string]string {
	return map[string]string{
		"scope_uuid": e.scopeUUID,
		"asset_type": e.assetType,
		"asset_uuid": e.assetUUID,
	}
}

//Error implements the tasks.sentryException interface.
func (e setAssetSizeError) Error() string {
	return fmt.Sprintf("cannot resize %s %s to size %d: %s", e.assetType, e.assetUUID, e.newSize, e.inner.Error())
}
