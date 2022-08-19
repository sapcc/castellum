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
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestCollectGarbage(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, _ *plugins.AssetManagerStatic, _ *test.FakeClock) {
		fakeNow := time.Unix(0, 0).UTC()

		//setup some minimal scaffolding (we can only insert finished_operations
		//with valid asset IDs into the DB)
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID: "project1",
			AssetType: "foo",
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID: 1,
			UUID:       "asset1",
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID: 1,
			UUID:       "asset2",
		}))

		ops := []db.FinishedOperation{
			{
				AssetID:    1,
				Reason:     db.OperationReasonHigh,
				Outcome:    db.OperationOutcomeCancelled,
				OldSize:    1000,
				NewSize:    1200,
				Usage:      db.UsageValues{db.SingularUsageMetric: 800},
				CreatedAt:  fakeNow.Add(-40 * time.Minute),
				FinishedAt: fakeNow.Add(-30 * time.Minute),
			},
			{
				AssetID:    2,
				Reason:     db.OperationReasonHigh,
				Outcome:    db.OperationOutcomeCancelled,
				OldSize:    1000,
				NewSize:    1200,
				Usage:      db.UsageValues{db.SingularUsageMetric: 800},
				CreatedAt:  fakeNow.Add(-25 * time.Minute),
				FinishedAt: fakeNow.Add(-20 * time.Minute),
			},
			{
				AssetID:     2,
				Reason:      db.OperationReasonCritical,
				Outcome:     db.OperationOutcomeSucceeded,
				OldSize:     1000,
				NewSize:     1200,
				Usage:       db.UsageValues{db.SingularUsageMetric: 800},
				CreatedAt:   fakeNow.Add(-20 * time.Minute),
				ConfirmedAt: p2time(fakeNow.Add(-20 * time.Minute)),
				GreenlitAt:  p2time(fakeNow.Add(-20 * time.Minute)),
				FinishedAt:  fakeNow.Add(-10 * time.Minute),
			},
		}
		for _, op := range ops {
			t.Must(c.DB.Insert(&op)) //nolint:gosec // Insert is not holding onto the pointer after it returns
		}

		t.ExpectFinishedOperations(c.DB, ops...)
		t.Must(CollectGarbage(c.DB, fakeNow.Add(-15*time.Minute)))
		t.ExpectFinishedOperations(c.DB, ops[2])
	})
}
