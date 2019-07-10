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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestCaptureSentryException(t *testing.T) {
	dsn := os.Getenv("CASTELLUM_TEST_SENTRY_DSN")
	if dsn == "" {
		return
	}

	err := sentry.Init(sentry.ClientOptions{Dsn: dsn})
	if err != nil {
		t.Errorf("Sentry initialization failed: %s", err.Error())
	}

	var (
		scope1 = "323dd776-bace-435b-ab96-b70f5dab2fbb"
		scope2 = "4e75bf14-3fa2-4ca3-a72a-1d7e21adf1e9"

		type1 = "foo"
		type2 = "bar"
	)

	tt := []sentryException{
		getAssetStatusError{
			scopeUUID: scope1,
			assetType: type1,
			assetUUID: "56a1a004-a8ec-42f0-b993-4f72d9c8ae0b",
			inner:     errors.New("this is a test asset scrape error message"),
		},
		getAssetStatusError{
			scopeUUID: scope2,
			assetType: type2,
			assetUUID: "bb055a4e-0098-410a-a45f-52cf6c18c0a2",
			inner:     errors.New("this is a test asset scrape error message"),
		},

		setAssetSizeError{
			scopeUUID: scope1,
			assetType: type1,
			assetUUID: "a66b007a-d3fa-4561-9551-f0203ece6e08",
			newSize:   0,
			inner:     errors.New("this is a test asset resize error message"),
		},
		setAssetSizeError{
			scopeUUID: scope2,
			assetType: type2,
			assetUUID: "799480b8-b3f1-4f11-8fbf-6eb1e6c0797f",
			newSize:   0,
			inner:     errors.New("this is a test asset resize error message"),
		},

		listAssetsError{
			scopeUUID: scope2,
			assetType: type1,
			inner:     errors.New("this is a test resource scrape error message"),
		},
		listAssetsError{
			scopeUUID: scope1,
			assetType: type2,
			inner:     errors.New("this is a test resource scrape error message"),
		},
	}

	hub := sentry.CurrentHub()
	for _, tc := range tt {
		captureSentryException(hub, tc)
		time.Sleep(time.Second * 1)
		captureSentryException(hub, tc)
	}
	sentry.Flush(time.Second * 10) // wait until all events are sent or timeout is reached
}
