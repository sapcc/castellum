/******************************************************************************
*
*  Copyright 2020 SAP SE
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

package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sapcc/castellum/internal/db"
)

// Config contains the app-level configuration options.
type Config struct {
	MaxAssetSizeRules []MaxAssetSizeRule
}

type MaxAssetSizeRule struct {
	Regex *regexp.Regexp
	Value *uint64
}

func (c Config) MaxAssetSizeFor(AssetType db.AssetType) (maxAssetSize *uint64) {
	for _, rule := range c.MaxAssetSizeRules {
		if rule.Regex.MatchString(string(AssetType)) {
			if maxAssetSize == nil || *rule.Value < *maxAssetSize {
				maxAssetSize = rule.Value
			}
		}
	}

	return maxAssetSize
}

func (c *Config) SetMaxAssetSizeRules(maxAssetSizeString string) error {
	maxAssetSizes := strings.Split(maxAssetSizeString, ",")
	for _, v := range maxAssetSizes {
		sL := strings.Split(v, "=")
		if len(sL) != 2 {
			return fmt.Errorf("expected a max asset size configuration value in the format: '<asset-type>=<max-asset-size>', got: %s", v)
		}
		assetType := sL[0]
		maxSize, err := strconv.ParseUint(sL[1], 10, 64)
		if err != nil {
			return err
		}

		rgx, err := regexp.Compile("^" + assetType + "$")
		if err != nil {
			return err
		}
		c.MaxAssetSizeRules = append(c.MaxAssetSizeRules, MaxAssetSizeRule{
			Regex: rgx,
			Value: &maxSize,
		})
	}
	return nil
}
