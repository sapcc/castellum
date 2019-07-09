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

func setupAssetScrapeTest(t test.T) (*Context, func(plugins.StaticAsset), *test.FakeClock) {
	c, amStatic, clock := setupContext(t)

	//ScrapeNextAsset() without any resources just does nothing
	err := c.ScrapeNextAsset("foo", c.TimeNow())
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	easypg.AssertDBContent(t.T, c.DB.Db, "fixtures/resource-scrape-0.sql")

	//create a resource and asset to test with
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

	return c, setAsset, clock
}

func TestNoOperationWhenNoThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	c, _, clock := setupAssetScrapeTest(t)

	//when no threshold is crossed, no operation gets created
	clock.StepBy(10 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestNormalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//set a maximum size that does not contradict the following operations
	//(down below, there's a separate test for when the maximum size actually
	//inhibits upsizing)
	t.MustExec(c.DB, `UPDATE resources SET max_size = 2000`)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow(),
	}
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 820})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 840})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	expectedOp.ConfirmedAt = p2time(c.TimeNow())
	expectedOp.GreenlitAt = p2time(c.TimeNow())
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 780})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestNormalUpsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow(),
	})
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 790})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestNormalDownsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//set a minimum size that does not contradict the following operations
	//(down below, there's a separate test for when the minimum size actually
	//inhibits upsizing)
	t.MustExec(c.DB, `UPDATE resources SET min_size = 200`)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow(),
	}
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 180})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 160})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	expectedOp.ConfirmedAt = p2time(c.TimeNow())
	expectedOp.GreenlitAt = p2time(c.TimeNow())
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 220})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestNormalDownsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow(),
	})
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 210})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestCriticalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//when the "Critical" threshold gets crossed, a "Critical" operation gets
	//created and immediately confirmed/greenlit
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 950})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 95,
		CreatedAt:    c.TimeNow(),
		ConfirmedAt:  p2time(c.TimeNow()),
		GreenlitAt:   p2time(c.TimeNow()),
	}
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestReplaceNormalWithCriticalUpsize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow(),
	})
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//when the "Critical" threshold gets crossed while the the "High" operation
	//is not yet confirmed, the "High" operation is cancelled and a "Critical"
	//operation replaces it
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 960})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           2,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
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
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestAssetScrapeOrdering(baseT *testing.T) {
	t := test.T{T: baseT}
	c, amStatic, clock := setupContext(t)

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
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

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
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
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
}

func TestNoOperationWhenNoSizeChange(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//when size is already at the lowest possible value (1), no new operation
	//shall be created even if the usage is below the "low" threshold -- there is
	//just nothing to resize to
	clock.StepBy(5 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1, Usage: 0})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
}

func TestAssetScrapeReflectingResizeOperationWithDelay(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

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
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	asset.CheckedAt = c.TimeNow()
	asset.ScrapedAt = p2time(c.TimeNow())
	t.ExpectAssets(c.DB, asset)

	t.ExpectPendingOperations(c.DB /*, nothing */)

	//second scrape will see the new size and update the asset accordingly, and
	//it will also create an operation because the usage is still above 80% after
	//the resize
	clock.StepBy(5 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	asset.Size = 1100
	asset.AbsoluteUsage = p2uint64(1000)
	asset.UsagePercent = 90
	asset.CheckedAt = c.TimeNow()
	asset.ScrapedAt = p2time(c.TimeNow())
	asset.ExpectedSize = nil
	t.ExpectAssets(c.DB, asset)

	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1100,
		NewSize:      1320,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow(),
	})
}

func TestAssetScrapeObservingNewSizeWhileWaitingForResize(baseT *testing.T) {
	//This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	//but we simulate an unrelated user-triggered resize operation taking place
	//in parallel with Castellum's resize operation, so we observe a new size
	//that's different from the expected size.
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//make asset look like it just completed a resize operation
	t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100`)
	setAsset(plugins.StaticAsset{
		Size:  1200, //!= asset.ExpectedSize (see above)
		Usage: 600,
	})

	clock.StepBy(5 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
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
}

func TestAssetScrapeWithGetAssetStatusError(baseT *testing.T) {
	//This tests the behavior when GetAssetStatus returns an error. The error is
	//passed through to the caller of ScrapeNextAsset, but the asset's checked_at
	//timestamp is still updated to ensure that the main loop progresses to the
	//next asset.
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	setAsset(plugins.StaticAsset{
		Size:                 1000,
		Usage:                600,
		CannotGetAssetStatus: true,
	})

	clock.StepBy(5 * time.Minute)
	err := c.ScrapeNextAsset("foo", c.TimeNow())
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
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

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
}

func TestSkipDownsizeBecauseOfMinimumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//configure a minimum size on the resource
	t.MustExec(c.DB, `UPDATE resources SET min_size = 900`)

	//set a usage that is ripe for downsizing
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

	//ScrapeNextAsset should *not* create a downsize operation because the
	//minimum size would be crossed
	clock.StepBy(5 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestSkipUpsizeBecauseOfMaximumSize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//configure a maximum size on the resource
	t.MustExec(c.DB, `UPDATE resources SET max_size = 1100`)

	//set a usage that is ripe for upsizing, even for critical upsizing
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 999})

	//ScrapeNextAsset should *not* create any operations because the
	//maximum size would be crossed
	clock.StepBy(5 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestExternalResizeWhileOperationPending(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//create a "High" operation
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow(),
	}
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)

	//while it is not greenlit yet, simulate a resize operation
	//being performed by an unrelated user
	clock.StepBy(10 * time.Minute)
	setAsset(plugins.StaticAsset{Size: 1100, Usage: 900}) // bigger, but still >80% usage
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))

	//ScrapeNextAsset should have adjusted the NewSize to CurrentSize + SizeStep
	expectedOp.NewSize = 1320
	t.ExpectPendingOperations(c.DB, expectedOp)
	t.ExpectFinishedOperations(c.DB /*, nothing */)
}

func TestDownsizeAllowedByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//set a very low usage that permits downsizing
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

	//configure a MinimumFreeSize such that the downsizing operation is still
	//permitted by it (see next testcase for the opposite behavior)
	t.MustExec(c.DB, `UPDATE resources SET min_free_size = 600`)

	//ScrapeNextAsset should create a downsize operation
	clock.StepBy(10 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 10,
		CreatedAt:    c.TimeNow(),
	})
}

func TestDownsizeForbiddenByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, setAsset, clock := setupAssetScrapeTest(t)

	//set a very low usage that permits downsizing
	setAsset(plugins.StaticAsset{Size: 1000, Usage: 100})

	//configure a MinimumFreeSize that inhibits this downsizing operation (note
	//that while the *current* free size is larger than 800, the *new* free size
	//would be lower, and that's the crucial point)
	t.MustExec(c.DB, `UPDATE resources SET min_free_size = 800`)

	//ScrapeNextAsset should NOT create a downsize operation
	clock.StepBy(10 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
}

func TestUpsizeForcedByMinimumFreeSize(baseT *testing.T) {
	t := test.T{T: baseT}
	c, _, clock := setupAssetScrapeTest(t)

	//the asset starts out at size = 1000, usage = 500, which wouldn't warrant an
	//upsize; set a MinimumFreeSize larger than the current free size to force
	//upsizing
	t.MustExec(c.DB, `UPDATE resources SET min_free_size = 600`)

	//ScrapeNextAsset should therefore create an upsize operation
	clock.StepBy(10 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 50,
		CreatedAt:    c.TimeNow(),
	})

	//to double-check, remove the reason for the upsize operation
	t.MustExec(c.DB, `UPDATE resources SET min_free_size = 500`)

	//ScrapeNextAsset should cancel the operation
	clock.StepBy(10 * time.Minute)
	t.Must(c.ScrapeNextAsset("foo", c.TimeNow()))
	t.ExpectPendingOperations(c.DB /*, nothing */)
}
