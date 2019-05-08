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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func setupAssetResizeTest(t test.T, c *Context, amStatic *plugins.AssetManagerStatic, assetCount int) {
	//create a resource and assets to test with
	t.Must(c.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {},
	}

	for idx := 1; idx <= assetCount; idx++ {
		uuid := fmt.Sprintf("asset%d", idx)
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID:   1,
			UUID:         uuid,
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    c.TimeNow(),
			Stale:        false,
		}))

		amStatic.Assets["project1"][uuid] = plugins.StaticAsset{
			Size:  1000,
			Usage: 500,
		}
	}
}

func TestSuccessfulResize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, amStatic, clock := setupContext(t)
	setupAssetResizeTest(t, c, amStatic, 1)

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
	t.Must(c.DB.Insert(&pendingOp))

	//ExecuteNextResize() should do nothing right now because that operation is
	//only greenlit in the future, but not right now
	err := c.ExecuteNextResize()
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	t.ExpectPendingOperations(c.DB, pendingOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//go into the future and check that the operation gets executed
	clock.StepBy(10 * time.Minute)
	t.Must(c.ExecuteNextResize())
	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
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

	//expect asset to be marked as stale, but still show the old size (until the
	//next asset scrape)
	t.ExpectAssets(c.DB, db.Asset{
		ID:           1,
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		UsagePercent: 50,
		ScrapedAt:    c.TimeNow().Add(-15 * time.Minute),
		Stale:        true,
	})
}

func TestFailingResize(tBase *testing.T) {
	t := test.T{T: tBase}
	c, amStatic, clock := setupContext(t)
	setupAssetResizeTest(t, c, amStatic, 1)

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
	t.Must(c.DB.Insert(&pendingOp))

	//check that resizing fails as expected
	t.Must(c.ExecuteNextResize())
	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
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

	//check that asset was not marked as stale
	t.ExpectAssets(c.DB, db.Asset{
		ID:           1,
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		UsagePercent: 50,
		ScrapedAt:    c.TimeNow().Add(-10 * time.Minute),
		Stale:        false,
	})
}

func TestOperationQueueBehavior(baseT *testing.T) {
	t := test.T{T: baseT}
	//This test checks that, when there are multiple operations to execute, each
	//operation gets executed /exactly once/.
	c, amStatic, clock := setupContext(t)
	setupAssetResizeTest(t, c, amStatic, 10)

	//add 10 pending operations that are all ready to execute immediately
	clock.StepBy(10 * time.Minute)
	var finishedOps []db.FinishedOperation
	for idx := uint64(1); idx <= 10; idx++ {
		pendingOp := db.PendingOperation{
			AssetID:      int64(idx),
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      1200 + idx, //need operations to be distinguishable
			UsagePercent: 50,
			CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
			ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
			GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
		}
		t.Must(c.DB.Insert(&pendingOp))
		finishedOps = append(finishedOps,
			pendingOp.IntoFinishedOperation(db.OperationOutcomeSucceeded, c.TimeNow()),
		)
	}

	//execute them all in parallel
	blocker := make(chan struct{})
	c.Blocker = blocker
	wg := &sync.WaitGroup{}
	wg.Add(10)
	for idx := 0; idx < 10; idx++ {
		go func() {
			defer wg.Done()
			t.Must(c.ExecuteNextResize())
		}()
	}

	close(blocker)
	wg.Wait()
	t.ExpectFinishedOperations(c.DB, finishedOps...)
}
