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
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/easypg"
)

func runAssetScrapeTest(t test.T, resourceIsSingleStep bool, action func(*Context, func(plugins.StaticAsset), *test.FakeClock)) {
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {

		//ScrapeNextAsset() without any resources just does nothing
		err := c.ScrapeNextAsset(c.TimeNow())
		if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		_, dbDump := easypg.NewTracker(t.T, c.DB.Db)
		dbDump.AssertEmpty()

		//create a resource and asset to test with
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			AssetType:                "foo",
			LowThresholdPercent:      20,
			LowDelaySeconds:          3600,
			HighThresholdPercent:     80,
			HighDelaySeconds:         3600,
			CriticalThresholdPercent: 95,
			SizeStepPercent:          ifthenelseF64(resourceIsSingleStep, 0, 20),
			SingleStep:               resourceIsSingleStep,
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID:    1,
			UUID:          "asset1",
			Size:          1000,
			AbsoluteUsage: p2uint64(500),
			UsagePercent:  50,
			CheckedAt:     c.TimeNow(),
			ScrapedAt:     p2time(c.TimeNow()),
			ExpectedSize:  nil,
		}))

		//setup asset with configurable size
		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 500},
			},
		}
		setAsset := func(a plugins.StaticAsset) {
			amStatic.Assets["project1"]["asset1"] = a
		}

		action(c, setAsset, clock)
	})
}

func forAllSteppingStrategies(t test.T, action func(*Context, db.Resource, func(plugins.StaticAsset), *test.FakeClock)) {
	runAssetScrapeTest(t, false, func(c *Context, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {
		var res db.Resource
		t.Must(c.DB.SelectOne(&res, `SELECT * FROM resources WHERE id = 1`))
		t.Log("running testcase with percentage-step resizing")
		action(c, res, setAsset, clock)
	})

	runAssetScrapeTest(t, true, func(c *Context, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {
		var res db.Resource
		t.Must(c.DB.SelectOne(&res, `SELECT * FROM resources WHERE id = 1`))
		t.Log("running testcase with single-step resizing")
		action(c, res, setAsset, clock)
	})
}

func TestNoOperationWhenNoThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when no threshold is crossed, no operation gets created
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestNormalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a maximum size that does not contradict the following operations
		//(down below, there's a separate test for when the maximum size actually
		//inhibits upsizing)
		t.MustExec(c.DB, `UPDATE resources SET max_size = 2000`)

		//when the "High" threshold gets crossed, a "High" operation gets created in
		//state "created"
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		expectedOp := db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1001, 1200),
			UsagePercent: 80,
			CreatedAt:    c.TimeNow(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//another scrape while the delay is not over should not change the state
		//(but for single-step resizing which takes the current usage into account,
		//the NewSize is updated to put the target size outside of the high
		//threshold again)
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 820})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		if res.SingleStep {
			expectedOp.NewSize = 1026
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 840})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		expectedOp.ConfirmedAt = p2time(c.TimeNow())
		expectedOp.GreenlitAt = p2time(c.TimeNow())
		if res.SingleStep {
			expectedOp.NewSize = 1051
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//since the operation is now greenlit and can be picked up by a worker at any
		//moment, we should not touch it anymore even if the reason disappears
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 780})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestNormalUpsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when the "High" threshold gets crossed, a "High" operation gets created in
		//state "created"
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1001, 1200),
			UsagePercent: 80,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//when the reason disappears within the delay, the operation is cancelled
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 790})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			Outcome:      db.OperationOutcomeCancelled,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1001, 1200),
			UsagePercent: 80,
			CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
			FinishedAt:   c.TimeNow(),
		})

	})
}

func TestNormalDownsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a minimum size that does not contradict the following operations
		//(down below, there's a separate test for when the minimum size actually
		//inhibits upsizing)
		t.MustExec(c.DB, `UPDATE resources SET min_size = 200`)

		//when the "Low" threshold gets crossed, a "Low" operation gets created in
		//state "created"
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		expectedOp := db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 999, 800),
			UsagePercent: 20,
			CreatedAt:    c.TimeNow(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//another scrape while the delay is not over should not change the state
		//(but for single-step resizing which takes the current usage into account,
		//the NewSize is updated to put the target size above the low threshold
		//again)
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 180})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		if res.SingleStep {
			expectedOp.NewSize = 899
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 160})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		if res.SingleStep {
			expectedOp.NewSize = 799
		}
		expectedOp.ConfirmedAt = p2time(c.TimeNow())
		expectedOp.GreenlitAt = p2time(c.TimeNow())
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//since the operation is now greenlit and can be picked up by a worker at any
		//moment, we should not touch it anymore even if the reason disappears
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 220})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestNormalDownsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when the "Low" threshold gets crossed, a "Low" operation gets created in
		//state "created"
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 999, 800),
			UsagePercent: 20,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//when the reason disappears within the delay, the operation is cancelled
		clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 210})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			Outcome:      db.OperationOutcomeCancelled,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 999, 800),
			UsagePercent: 20,
			CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
			FinishedAt:   c.TimeNow(),
		})

	})
}

func TestCriticalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when the "Critical" threshold gets crossed, a "Critical" operation gets
		//created and immediately confirmed/greenlit
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 950})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		expectedOp := db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonCritical,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1188, 1200),
			UsagePercent: 95,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestReplaceNormalWithCriticalUpsize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when the "High" threshold gets crossed, a "High" operation gets created in
		//state "created"
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1126, 1200),
			UsagePercent: 90,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//when the "Critical" threshold gets crossed while the the "High" operation
		//is not yet confirmed, the "High" operation is cancelled and a "Critical"
		//operation replaces it
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 960})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           2,
			AssetID:      1,
			Reason:       db.OperationReasonCritical,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1201, 1200),
			UsagePercent: 96,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		})
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			Outcome:      db.OperationOutcomeCancelled,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1126, 1200),
			UsagePercent: 90,
			CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
			FinishedAt:   c.TimeNow(),
		})

	})
}

func TestAssetScrapeOrdering(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {

		//create a resource and multiple assets to test with
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			AssetType:                "foo",
			LowThresholdPercent:      20,
			LowDelaySeconds:          3600,
			HighThresholdPercent:     80,
			HighDelaySeconds:         3600,
			CriticalThresholdPercent: 95,
			SizeStepPercent:          20,
		}))
		assets := []db.Asset{
			{
				ResourceID:   1,
				UUID:         "asset1",
				Size:         1000,
				UsagePercent: 50,
				CheckedAt:    c.TimeNow(),
				ScrapedAt:    p2time(c.TimeNow()),
				ExpectedSize: nil,
			},
			{
				ResourceID:   1,
				UUID:         "asset2",
				Size:         1000,
				UsagePercent: 50,
				CheckedAt:    c.TimeNow(),
				ScrapedAt:    p2time(c.TimeNow()),
				ExpectedSize: nil,
			},
			{
				ResourceID:   1,
				UUID:         "asset3",
				Size:         1000,
				UsagePercent: 50,
				CheckedAt:    c.TimeNow(),
				ScrapedAt:    p2time(c.TimeNow()),
				ExpectedSize: nil,
			},
		}
		t.Must(c.DB.Insert(&assets[0]))
		t.Must(c.DB.Insert(&assets[1]))
		t.Must(c.DB.Insert(&assets[2]))

		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 510},
				"asset2": {Size: 1000, Usage: 520},
				"asset3": {Size: 1000, Usage: 530},
			},
		}

		//this should scrape each asset once, in order
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		//so the asset table should look like this now
		assets[0].CheckedAt = c.TimeNow().Add(-2 * time.Minute)
		assets[1].CheckedAt = c.TimeNow().Add(-time.Minute)
		assets[2].CheckedAt = c.TimeNow()
		assets[0].AbsoluteUsage = p2uint64(510)
		assets[1].AbsoluteUsage = p2uint64(520)
		assets[2].AbsoluteUsage = p2uint64(530)
		assets[0].UsagePercent = 51
		assets[1].UsagePercent = 52
		assets[2].UsagePercent = 53
		for idx := 0; idx < len(assets); idx++ {
			assets[idx].ScrapedAt = p2time(assets[idx].CheckedAt)
		}
		t.ExpectAssets(c.DB, assets...)

		//next scrape should work identically
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		clock.StepBy(time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		assets[0].CheckedAt = c.TimeNow().Add(-2 * time.Minute)
		assets[1].CheckedAt = c.TimeNow().Add(-time.Minute)
		assets[2].CheckedAt = c.TimeNow()
		for idx := 0; idx < len(assets); idx++ {
			assets[idx].ScrapedAt = p2time(assets[idx].CheckedAt)
		}
		t.ExpectAssets(c.DB, assets...)

		//and all of this should not have created any operations
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestNoOperationWhenNoSizeChange(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//when size is already at the lowest possible value (1), no new operation
		//shall be created even if the usage is below the "low" threshold -- there is
		//just nothing to resize to
		clock.StepBy(5 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1, Usage: 0})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)

	})
}

func TestAssetScrapeReflectingResizeOperationWithDelay(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//make asset look like it just completed a resize operation
		t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100`)
		setAsset(plugins.StaticAsset{
			Size:           1000,
			Usage:          1000,
			NewSize:        1100,
			RemainingDelay: 2,
		})
		asset := db.Asset{
			ID:            1,
			ResourceID:    1,
			UUID:          "asset1",
			Size:          1000,
			AbsoluteUsage: p2uint64(500),
			UsagePercent:  50,
			CheckedAt:     c.TimeNow(),
			ScrapedAt:     p2time(c.TimeNow()),
			ExpectedSize:  p2uint64(1100),
		}

		//first scrape will not touch anything about the asset, and also not create
		//any operations (even though it could because of the currently high usage)
		//because the backend does not yet reflect the changed size
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		asset.CheckedAt = c.TimeNow()
		asset.ScrapedAt = p2time(c.TimeNow())
		t.ExpectAssets(c.DB, asset)

		t.ExpectPendingOperations(c.DB /*, nothing */)

		//second scrape will see the new size and update the asset accordingly, and
		//it will also create an operation because the usage is still above 80% after
		//the resize
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		asset.Size = 1100
		asset.AbsoluteUsage = p2uint64(1000)
		asset.UsagePercent = 1000. / 11.
		asset.CheckedAt = c.TimeNow()
		asset.ScrapedAt = p2time(c.TimeNow())
		asset.ExpectedSize = nil
		t.ExpectAssets(c.DB, asset)

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1100,
			NewSize:      ifthenelseU64(res.SingleStep, 1251, 1320),
			UsagePercent: 1000. / 11.,
			CreatedAt:    c.TimeNow(),
		})

	})
}

func TestAssetScrapeObservingNewSizeWhileWaitingForResize(baseT *testing.T) {
	//This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	//but we simulate an unrelated user-triggered resize operation taking place
	//in parallel with Castellum's resize operation, so we observe a new size
	//that's different from the expected size.
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//make asset look like it just completed a resize operation
		t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100`)
		setAsset(plugins.StaticAsset{
			Size:  1200, //!= asset.ExpectedSize (see above)
			Usage: 600,
		})

		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectAssets(c.DB, db.Asset{
			ID:            1,
			ResourceID:    1,
			UUID:          "asset1",
			Size:          1200,
			AbsoluteUsage: p2uint64(600),
			UsagePercent:  50,
			CheckedAt:     c.TimeNow(),
			ScrapedAt:     p2time(c.TimeNow()),
			ExpectedSize:  nil,
		})

	})
}

func TestAssetScrapeWithGetAssetStatusError(baseT *testing.T) {
	//This tests the behavior when GetAssetStatus returns an error:
	//1. If core.AssetNotFoundErr is returned then the asset is deleted from
	//the db.
	//2. All other errors are passed through to the caller of ScrapeNextAsset,
	//but the asset's checked_at timestamp is still updated to ensure that the
	//main loop progresses to the next asset.
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		setAsset(plugins.StaticAsset{
			Size:                 1000,
			Usage:                600,
			CannotGetAssetStatus: true,
		})

		clock.StepBy(5 * time.Minute)
		err := c.ScrapeNextAsset(c.TimeNow())
		expectedMsg := "cannot query status of foo asset1: GetAssetStatus failing as requested"
		if err == nil {
			t.Error("ScrapeNextAsset should have failed here")
		} else if err.Error() != expectedMsg {
			t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
		}

		t.ExpectAssets(c.DB, db.Asset{
			ID:                 1,
			ResourceID:         1,
			UUID:               "asset1",
			Size:               1000,
			AbsoluteUsage:      p2uint64(500),
			UsagePercent:       50,                                        //changed usage not observed because of error
			ScrapedAt:          p2time(c.TimeNow().Add(-5 * time.Minute)), //not updated by ScrapeNextAsset!
			CheckedAt:          c.TimeNow(),                               //but this WAS updated!
			ExpectedSize:       nil,
			ScrapeErrorMessage: "GetAssetStatus failing as requested",
		})

		//when GetAssetStatus starts working again, next ScrapeNextAsset should clear
		//the error field
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600})
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectAssets(c.DB, db.Asset{
			ID:                 1,
			ResourceID:         1,
			UUID:               "asset1",
			Size:               1000,
			AbsoluteUsage:      p2uint64(600),
			UsagePercent:       60,
			ScrapedAt:          p2time(c.TimeNow()),
			CheckedAt:          c.TimeNow(),
			ExpectedSize:       nil,
			ScrapeErrorMessage: "",
		})

		//Note: this test should be at the end, see below.
		//Run GetAssetStatus on the same asset again except this time the
		//ScrapeNextAsset should delete the asset from the db.
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600, CannotFindAsset: true})
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectAssets(c.DB)

	})
}

func TestSkipDownsizeBecauseOfMinimumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//configure a minimum size on the resource
		t.MustExec(c.DB, `UPDATE resources SET min_size = 1000`)

		//set a usage that is ripe for downsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

		//ScrapeNextAsset should *not* create a downsize operation because the
		//minimum size would be crossed
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestRestrictDownsizeBecauseOfMinimumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//configure a minimum size on the resource
		t.MustExec(c.DB, `UPDATE resources SET min_size = 900`)

		//set a usage that is ripe for downsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

		//ScrapeNextAsset should create a downsize operation with new size 900,
		//even though percentage-step resizing would like to go to 800
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      900,
			UsagePercent: 10,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestSkipUpsizeBecauseOfMaximumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//configure a maximum size on the resource
		t.MustExec(c.DB, `UPDATE resources SET max_size = 1000`)

		//set a usage that is ripe for upsizing, even for critical upsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 999})

		//ScrapeNextAsset should *not* create any operations because the
		//maximum size would be crossed
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestRestrictUpsizeBecauseOfMaximumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//configure a maximum size on the resource
		t.MustExec(c.DB, `UPDATE resources SET max_size = 1100`)

		//set a usage that is ripe for upsizing, even for critical upsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 999})

		//ScrapeNextAsset should create a critical upsize operation: With either
		//stepping strategy, the desired target size is greater than the max_size,
		//so the max_size is chosen instead.
		clock.StepBy(5 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonCritical,
			OldSize:      1000,
			NewSize:      1100,
			UsagePercent: 99.9,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestExternalResizeWhileOperationPending(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//create a "High" operation
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		expectedOp := db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1126, 1200),
			UsagePercent: 90,
			CreatedAt:    c.TimeNow(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//while it is not greenlit yet, simulate a resize operation
		//being performed by an unrelated user
		clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1100, Usage: 900}) // bigger, but still >80% usage
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		//ScrapeNextAsset should have adjusted the NewSize to CurrentSize + SizeStep
		expectedOp.NewSize = ifthenelseU64(res.SingleStep, 1126, 1320)
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestDownsizeAllowedByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a very low usage that permits downsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

		//configure a MinimumFreeSize such that the downsizing operation is still
		//permitted by it (see next testcase for the opposite behavior)
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 600`)

		//ScrapeNextAsset should create a downsize operation
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 700, 800),
			UsagePercent: 10,
			CreatedAt:    c.TimeNow(),
		})

	})
}

func TestDownsizeRestrictedByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a very low usage that permits downsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

		//configure a MinimumFreeSize that restricts this downsizing operation to
		//only go to 900 instead of 800 (for percentage-step resizing) or 500 (for
		//single-step resizing)
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 800`)

		//ScrapeNextAsset should NOT create a downsize operation
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonLow,
			OldSize:      1000,
			NewSize:      900,
			UsagePercent: 10,
			CreatedAt:    c.TimeNow(),
		})

	})
}

func TestDownsizeForbiddenByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a very low usage that permits downsizing
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})

		//configure a MinimumFreeSize that inhibits this downsizing operation
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 800`)

		//ScrapeNextAsset should NOT create a downsize operation
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)

	})
}

func TestUpsizeForcedByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//the asset starts out at size = 1000, usage = 500, which wouldn't warrant an
		//upsize; set a MinimumFreeSize larger than the current free size to force
		//upsizing
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 600`)

		//ScrapeNextAsset should therefore create an upsize operation
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      ifthenelseU64(res.SingleStep, 1100, 1200),
			UsagePercent: 50,
			CreatedAt:    c.TimeNow(),
		})

		//to double-check, remove the reason for the upsize operation
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 500`)

		//ScrapeNextAsset should cancel the operation
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)

	})
}

func TestCriticalUpsizeTakingMultipleStepsAtOnce(baseT *testing.T) {
	t := test.T{T: baseT}
	//This test is specific to percentage-step resizing because single-step
	//resizing has no concept of "taking multiple steps at once".
	runAssetScrapeTest(t, false, func(c *Context, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set a very small step size
		t.MustExec(c.DB, `UPDATE resources SET size_step_percent = 1`)

		//set usage way above the critical threshold
		setAsset(plugins.StaticAsset{Size: 1380, Usage: 1350})

		//ScrapeNextAsset should create a "critical" operation taking four steps at
		//once (1380 -> 1393 -> 1406 -> 1420 -> 1434)
		//
		//This example is manufactured specifically such that the step size changes
		//between steps, to validate that a new step size is calculated each time,
		//same as if multiple steps had been taken in successive operations.
		//
		//NOTE: This testcase used to require a target size of 1420, but that was wrong.
		//A size of 1420 would lead to 95% usage (or rather, 95.07%) which is still
		//above the critical threshold.
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonCritical,
			OldSize:      1380,
			NewSize:      1434,
			UsagePercent: 13500. / 138.,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestZeroSizedAssetWithoutUsage(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//This may occur e.g. in the project-quota asset manager, when the project
		//in question has no quota at all. We expect Castellum to leave assets
		//with 0 size and 0 usage alone. And more importantly, we expect Castellum
		//to not crash on divide-by-zero while doing so. :)
		setAsset(plugins.StaticAsset{Size: 0, Usage: 0})

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectAssets(c.DB, db.Asset{
			ID:            1,
			ResourceID:    1,
			UUID:          "asset1",
			Size:          0,
			AbsoluteUsage: p2uint64(0),
			UsagePercent:  0,
			CheckedAt:     c.TimeNow(),
			ScrapedAt:     p2time(c.TimeNow()),
			ExpectedSize:  nil,
		})
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestZeroSizedAssetWithUsage(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//This may occur e.g. in the project-quota asset manager when the quota
		//setup is broken and there is usage without the requisite quota.
		setAsset(plugins.StaticAsset{Size: 0, Usage: 5})

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectAssets(c.DB, db.Asset{
			ID:            1,
			ResourceID:    1,
			UUID:          "asset1",
			Size:          0,
			AbsoluteUsage: p2uint64(5),
			UsagePercent:  200, //arbitrary value that represents non-zero usage on zero size
			CheckedAt:     c.TimeNow(),
			ScrapedAt:     p2time(c.TimeNow()),
			ExpectedSize:  nil,
		})
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:      1,
			AssetID: 1,
			Reason:  db.OperationReasonCritical,
			OldSize: 0,
			//single-step resizing will end up one higher than percentage-step
			//resizing because it also wants to leave the high threshold
			NewSize:      ifthenelseU64(res.SingleStep, 7, 6),
			UsagePercent: 200,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestDownsizeShouldNotGoIntoHighThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		t.MustExec(c.DB, `UPDATE resources SET low_threshold_percent = 75`)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 700})

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:      1,
			AssetID: 1,
			Reason:  db.OperationReasonLow,
			OldSize: 1000,
			//single-step resizing targets just above the low threshold and thus does
			//not come near the high threshold, but percentage-step resizing would
			//(if it ignored the high threshold) go down to size 800 which is too far
			NewSize:      ifthenelseU64(res.SingleStep, 933, 876),
			UsagePercent: 70,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestDownsizeShouldNotGoIntoCriticalThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//same as above, but test without high threshold
		t.MustExec(c.DB, `UPDATE resources SET low_threshold_percent = 90, high_threshold_percent = 0, high_delay_seconds = 0`)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:      1,
			AssetID: 1,
			Reason:  db.OperationReasonLow,
			OldSize: 1000,
			//single-step resizing targets just above the low threshold and thus does
			//not come near the critical threshold, but percentage-step resizing
			//would (if it ignored the critical threshold) go down to size 800 which
			//is too far
			NewSize:      ifthenelseU64(res.SingleStep, 888, 843),
			UsagePercent: 80,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestUpsizeShouldNotGoIntoHighThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//same as above, but in the opposite direction
		t.MustExec(c.DB, `UPDATE resources SET high_threshold_percent = 22`)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 230})

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:      1,
			AssetID: 1,
			Reason:  db.OperationReasonHigh,
			OldSize: 1000,
			//single-step resizing targets just below the high threshold and thus
			//does not come near the low threshold, but percentage-step resizing
			//would (if it ignored the low threshold) go up to size 1200 which is too
			//far
			NewSize:      ifthenelseU64(res.SingleStep, 1046, 1149),
			UsagePercent: 23,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

	})
}

func TestResizesEndingUpInLowThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//set thresholds very close to each other (this is what we recommend for
		//quota autoscaling, e.g. low = 99% and critical = 100%) -- this is usually
		//not a problem for large asset sizes because there is always a size value
		//that satisfies both constraints
		t.MustExec(c.DB, `UPDATE resources SET low_threshold_percent = 98, high_threshold_percent = 99, critical_threshold_percent = 100`)

		//BUT now we also choose a really small asset size, and a usage such that
		//there is no size value in the acceptable range of 98%-99% usage
		setAsset(plugins.StaticAsset{Size: 15, Usage: 14})

		//we are now below the low threshold, but no downsize should be generated
		//because downizing would put us above the high and critical thresholds
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		//BUT when we're inside the high/critical threshold, an upsize should be
		//generated even though upsizing puts us below the low threshold -- the
		//idea being that it's better to be slightly too large than slightly too
		//small
		setAsset(plugins.StaticAsset{Size: 14, Usage: 14})
		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))
		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonCritical,
			OldSize:      14,
			NewSize:      15,
			UsagePercent: 100,
			CreatedAt:    c.TimeNow(),
			ConfirmedAt:  p2time(c.TimeNow()),
			GreenlitAt:   p2time(c.TimeNow()),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestUpsizeBecauseOfMinFreeSizePassingLowThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	forAllSteppingStrategies(t, func(c *Context, res db.Resource, setAsset func(plugins.StaticAsset), clock *test.FakeClock) {

		//test that min_free_size takes precedence over the low usage threshold: we
		//should upsize the asset to guarantee the min_free_size, even if this puts
		//usage below the threshold
		t.MustExec(c.DB, `UPDATE resources SET min_free_size = 2500`)
		if !res.SingleStep {
			//for percentage-step resizing, we need to set the size step comically
			//large because we need to get below the low usage threshold to actually
			//trigger the condition we want to test
			t.MustExec(c.DB, `UPDATE resources SET size_step_percent = 200`)
		}

		clock.StepBy(10 * time.Minute)
		t.Must(c.ScrapeNextAsset(c.TimeNow()))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      3000,
			UsagePercent: 50,
			CreatedAt:    c.TimeNow(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func ifthenelseF64(cond bool, thenValue float64, elseValue float64) float64 {
	if cond {
		return thenValue
	}
	return elseValue
}

func ifthenelseU64(cond bool, thenValue uint64, elseValue uint64) uint64 {
	if cond {
		return thenValue
	}
	return elseValue
}
