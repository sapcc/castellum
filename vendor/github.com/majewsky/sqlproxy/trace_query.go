/******************************************************************************
*
* Copyright 2017-2018 SAP SE
* Copyright 2019 Stefan Majewsky <majewsky@gmx.net>
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
******************************************************************************/

package sqlproxy

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var sqlWhitespaceRx = regexp.MustCompile(`(?:\s|--.*)+`) // `.*` matches until end of line!

//TraceQuery produces a function that can be given to a sqlproxy.Driver as a
//BeforeQueryHook. It prints all executed SQL statements (including values
//bound to the statement) onto the given printer. The printer will be called
//exactly once per statement, and its argument will not contain any line
//breaks. For example:
//
//	sql.Register("postgres-with-logging", &sqlproxy.Driver {
//		ProxiedDriverName: "postgres",
//		BeforeQueryHook:   sqlproxy.TraceQuery(func(msg string) { log.Println(msg) }),
//	})
//
func TraceQuery(printer func(string)) func(string, []interface{}) {
	return func(query string, args []interface{}) {
		//simplify query string - remove comments and reduce whitespace
		//(This logic assumes that there are no arbitrary strings in the SQL
		//statement, which is okay since values should be given as args anyway.)
		query = strings.TrimSpace(sqlWhitespaceRx.ReplaceAllString(query, " "))

		//early exit for easy option
		if len(args) == 0 {
			printer(query)
			return
		}

		//if args contains time.Time objects, pretty-print these; use
		//fmt.Sprintf("%#v") for all other types of values
		argStrings := make([]string, len(args))
		for idx, argument := range args {
			switch arg := argument.(type) {
			case time.Time:
				argStrings[idx] = "time.Time [" + arg.Local().String() + "]"
			default:
				argStrings[idx] = fmt.Sprintf("%#v", arg)
			}
		}
		printer(query + " [" + strings.Join(argStrings, ", ") + "]")
	}
}
