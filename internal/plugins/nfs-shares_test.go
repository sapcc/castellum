// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	"github.com/sapcc/castellum/internal/db"
)

func TestMatchingNFSAssetType(t *testing.T) {
	tests := []struct {
		name            string
		shareTypeID     string
		shareTypeAssets map[string]db.AssetType
		allSharesAsset  db.AssetType
		hasAllShares    bool
		wantAssetType   db.AssetType
		wantMatch       bool
	}{
		{
			name:            "exact share type takes precedence",
			shareTypeID:     "type-id",
			shareTypeAssets: map[string]db.AssetType{"type-id": "nfs-shares-type:gold"},
			allSharesAsset:  "nfs-shares",
			hasAllShares:    true,
			wantAssetType:   "nfs-shares-type:gold",
			wantMatch:       true,
		},
		{
			name:           "falls back to all shares",
			shareTypeID:    "other-type-id",
			allSharesAsset: "nfs-shares",
			hasAllShares:   true,
			wantAssetType:  "nfs-shares",
			wantMatch:      true,
		},
		{
			name:        "ignores unmatched share",
			shareTypeID: "other-type-id",
			wantMatch:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotAssetType, gotMatch := matchingNFSAssetType(test.shareTypeID, test.shareTypeAssets, test.allSharesAsset, test.hasAllShares)
			if gotAssetType != test.wantAssetType || gotMatch != test.wantMatch {
				t.Errorf("matchingNFSAssetType() = (%q, %t), want (%q, %t)", gotAssetType, gotMatch, test.wantAssetType, test.wantMatch)
			}
		})
	}
}
