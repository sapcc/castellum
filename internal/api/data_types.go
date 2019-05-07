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
	"time"

	"github.com/sapcc/castellum/internal/db"
)

//Asset is how a db.Asset looks like in the API.
type Asset struct {
	UUID               int64        `json:"id"`
	Size               uint64       `json:"size"`
	UsagePercent       uint32       `json:"usage_percent"`
	ScrapedAt          time.Time    `json:"scraped_at"`
	Stale              bool         `json:"stale"`
	PendingOperation   *Operation   `json:"pending_operation,omitempty"`
	FinishedOperations *[]Operation `json:"finished_operations,omitempty"`
}

//Operation is how a db.PendingOperation or db.FinishedOperation looks like in
//the API.
type Operation struct {
	State     db.OperationState      `json:"state"`
	Reason    db.OperationReason     `json:"reason"`
	OldSize   uint64                 `json:"old_size"`
	NewSize   uint64                 `json:"new_size"`
	Created   OperationCreation      `json:"created"`
	Confirmed *OperationConfirmation `json:"confirmed,omitempty"`
	Greenlit  *OperationGreenlight   `json:"greenlit,omitempty"`
	Finished  *OperationFinish       `json:"finished,omitempty"`
}

//OperationCreation appears in type Operation.
type OperationCreation struct {
	At           time.Time `json:"at"`
	UsagePercent uint32    `json:"usage_percent"`
}

//OperationConfirmation appears in type Operation.
type OperationConfirmation struct {
	At time.Time `json:"at"`
}

//OperationGreenlight appears in type Operation.
type OperationGreenlight struct {
	At         time.Time `json:"at"`
	ByUserUUID string    `json:"by_user,omitempty"`
}

//OperationFinish appears in type Operation.
type OperationFinish struct {
	At           time.Time `json:"at"`
	ErrorMessage string    `json:"error,omitempty"`
}
