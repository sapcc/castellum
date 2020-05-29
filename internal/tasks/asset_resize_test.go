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
			ScrapedAt:    p2time(c.TimeNow()),
			ExpectedSize: nil,
		}))

		amStatic.Assets["project1"][uuid] = plugins.StaticAsset{
			Size:  1000,
			Usage: 500,
		}
	}
}

func TestSuccessfulResize(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {
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
		_, err := c.ExecuteNextResize()
		if err != sql.ErrNoRows {
			t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		t.ExpectPendingOperations(c.DB, pendingOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//go into the future and check that the operation gets executed
		clock.StepBy(10 * time.Minute)
		_, err = c.ExecuteNextResize()
		t.Must(err)
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

		//expect asset to report an expected size, but still show the old size
		//(until the next asset scrape)
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    p2time(c.TimeNow().Add(-15 * time.Minute)),
			ExpectedSize: p2uint64(1200),
		})
	})
}

func TestFailingResize(tBase *testing.T) {
	t := test.T{T: tBase}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {
		setupAssetResizeTest(t, c, amStatic, 1)

		//add a greenlit PendingOperation
		clock.StepBy(10 * time.Minute)
		pendingOp := db.PendingOperation{
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      600,
			UsagePercent: 50,
			CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
			ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
			GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
		}
		t.Must(c.DB.Insert(&pendingOp))

		amStatic.SetAssetSizeFails = true
		_, err := c.ExecuteNextResize()
		t.Must(err)

		//check that resizing fails as expected
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      600,
			UsagePercent: 50,
			CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
			ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
			GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
			FinishedAt:   c.TimeNow(),
			Outcome:      db.OperationOutcomeFailed,
			ErrorMessage: "SetAssetSize failing as requested",
		})

		//check that asset does not have an ExpectedSize
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    p2time(c.TimeNow().Add(-10 * time.Minute)),
			ExpectedSize: nil,
		})
	})
}

func TestErroringResize(tBase *testing.T) {
	t := test.T{T: tBase}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {
		setupAssetResizeTest(t, c, amStatic, 1)

		//add a greenlit PendingOperation that will error in SetAssetSize()
		clock.StepBy(10 * time.Minute)
		pendingOp := db.PendingOperation{
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      400, //will error because `new_size < usage` (usage = 500, see above)
			UsagePercent: 50,
			CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
			ConfirmedAt:  p2time(c.TimeNow().Add(-5 * time.Minute)),
			GreenlitAt:   p2time(c.TimeNow().Add(-5 * time.Minute)),
		}
		t.Must(c.DB.Insert(&pendingOp))

		//when the outcome of the resize is "errored", we can retry several times
		for try := 0; try < maxRetries; try++ {
			clock.StepBy(10 * time.Minute)
			_, err := c.ExecuteNextResize()
			t.Must(err)

			pendingOp.ID++
			pendingOp.ErroredAttempts++
			pendingOp.RetryAt = p2time(c.TimeNow().Add(retryInterval))
			t.ExpectPendingOperations(c.DB, pendingOp)
			t.ExpectFinishedOperations(c.DB /*, nothing */)
		}

		//ExecuteNextResize() should do nothing right now because, although the
		//operation is greenlit, its retry_at timestamp is in the future
		_, err := c.ExecuteNextResize()
		if err != sql.ErrNoRows {
			t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		t.ExpectPendingOperations(c.DB, pendingOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//check that resizing errors as expected once the retry budget is exceeded
		clock.StepBy(10 * time.Minute)
		_, err = c.ExecuteNextResize()
		t.Must(err)
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:         1,
			Reason:          db.OperationReasonLow,
			OldSize:         1000,
			NewSize:         400,
			UsagePercent:    50,
			CreatedAt:       pendingOp.CreatedAt,
			ConfirmedAt:     pendingOp.ConfirmedAt,
			GreenlitAt:      pendingOp.GreenlitAt,
			FinishedAt:      c.TimeNow(),
			Outcome:         db.OperationOutcomeErrored,
			ErrorMessage:    "cannot set size smaller than current usage",
			ErroredAttempts: maxRetries,
		})

		//check that asset does not have an ExpectedSize
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    p2time(c.TimeNow().Add(-(2 + maxRetries) * 10 * time.Minute)),
			ExpectedSize: nil,
		})
	})
}

func TestOperationQueueBehavior(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {
		//This test checks that, when there are multiple operations to execute, each
		//operation gets executed /exactly once/.
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
				_, err := c.ExecuteNextResize()
				t.Must(err)
			}()
		}

		close(blocker)
		wg.Wait()
		t.ExpectFinishedOperations(c.DB, finishedOps...)
	})
}
