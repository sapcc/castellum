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
	"database/sql"
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
)

func setupAssetResizeTest(t *testing.T, c *Context, amStatic *plugins.AssetManagerStatic) {
	//create a resource and asset to test with
	must(t, c.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	must(t, c.DB.Insert(&db.Asset{
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		UsagePercent: 50,
		ScrapedAt:    c.TimeNow(),
		Stale:        false,
	}))

	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {
				Size:  1000,
				Usage: 500,
			},
		},
	}
}

func TestSuccessfulResize(t *testing.T) {
	c, amStatic, clock := setupContext(t)
	setupAssetResizeTest(t, c, amStatic)

	//add a greenlit PendingOperation
	clock.StepBy(5 * time.Minute)
	pendingOp := db.PendingOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 50,
		CreatedAt:    c.TimeNow().Add(-5 * time.Minute),
		ConfirmedAt:  p2time(c.TimeNow()),
		GreenlitAt:   p2time(c.TimeNow().Add(5 * time.Minute)),
	}
	must(t, c.DB.Insert(&pendingOp))

	//ExecuteNextResize() should do nothing right now because that operation is
	//only greenlit in the future, but not right now
	err := c.ExecuteNextResize()
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	expectPendingOperations(t, c.DB, pendingOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//go into the future and check that the operation gets executed
	clock.StepBy(10 * time.Minute)
	must(t, c.ExecuteNextResize())
	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 50,
		CreatedAt:    c.TimeNow().Add(-15 * time.Minute),
		ConfirmedAt:  p2time(c.TimeNow().Add(-10 * time.Minute)),
		GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
		FinishedAt:   c.TimeNow(),
		Outcome:      db.OperationOutcomeSucceeded,
	})
}

func TestFailingResize(t *testing.T) {
	c, amStatic, clock := setupContext(t)
	setupAssetResizeTest(t, c, amStatic)

	//add a greenlit PendingOperation that will fail in SetAssetSize()
	clock.StepBy(10 * time.Minute)
	pendingOp := db.PendingOperation{
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      400, //will fail because `new_size < usage` (usage = 500, see above)
		UsagePercent: 50,
		CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
		ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
		GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
	}
	must(t, c.DB.Insert(&pendingOp))

	//check that resizing fails as expected
	must(t, c.ExecuteNextResize())
	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      400,
		UsagePercent: 50,
		CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
		ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
		GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
		FinishedAt:   c.TimeNow(),
		Outcome:      db.OperationOutcomeFailed,
		ErrorMessage: "cannot set size smaller than current usage",
	})
}

//TODO TestOperationQueueBehavior
