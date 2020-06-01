//  Copyright 2020 Winfried Klum
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v4"
)

var conn *pgx.Conn
var states = []string{"ENQUEUED", "PROCESSING", "WAITING", "FINISHED", "INVALID", "ERROR", "ALL"}
var validClassName = regexp.MustCompile(`^(?:\w+|\w+\.\w+)+$`)
var validLike = regexp.MustCompile(`^[^"'%]+$`)

const pgTimestampFormat = "2006-01-02 15:04:05.999999999"

func main() {
	var err error
	var args []string

	pArgs := readPipe()

	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	args = append(args, pArgs...)

	countCommand := flag.NewFlagSet("count", flag.ExitOnError)
	countStatePtr := countCommand.String("state", "ERROR", "workflow instance state. Possible states:"+fmt.Sprint(states))

	brokenCommand := flag.NewFlagSet("broken", flag.ExitOnError)
	brokenPatternPtr := brokenCommand.String("exception-pattern", "", "filter search pattern for broken workflow instance exception message")
	brokenStartTimePtr := brokenCommand.String("error-time-start", "", "filter on workflow error time, intervall start time in timestamp format, e.g. 2020-04-25 11:40:40.78")
	brokenEndTimePtr := brokenCommand.String("error-time-end", "", "filter on workflow error time, intervall end time in timestamp format, e.g. 2020-04-26 11:40:40.78")
	brokenCountTimerPtr := brokenCommand.Bool("print-count", false, "Print number of (filtered) broken workflow instances")
	brokenClassPtr := brokenCommand.String("workflow-class", "", "filter on workflow instance class, full package name required, e.g. org.foo.wf.MyWorkflow")

	dataCommand := flag.NewFlagSet("data", flag.ExitOnError)
	dataSelectorPtr := dataCommand.String("json-selector", "", "json selector, e.g. json->1='test', (only available for data in JSON format)")
	dataStatePtr := dataCommand.String("state", "ERROR", "workflow instance state. Possible states:"+fmt.Sprint(states))

	showCommand := flag.NewFlagSet("show", flag.ExitOnError)
	showDataPtr := showCommand.Bool("workflow-data", false, "show workflow instance data (only available for data in JSON format)")
	showAuditPtr := showCommand.Bool("audit-trail", false, "show audit trail messages")
	showInstancePtr := showCommand.Bool("instance-details", false, "show workflow instance details")
	showDataArrPtr := showCommand.Bool("print-data-array", false, "print workflow data list as json array, only valid in combination with -workflow-data flag")

	deleteCommand := flag.NewFlagSet("delete", flag.ExitOnError)

	restartCommand := flag.NewFlagSet("restart", flag.ExitOnError)

	cleanupCommand := flag.NewFlagSet("cleanup", flag.ExitOnError)
	cleanupAgePtr := cleanupCommand.String("age", "", "filter on age. Format as timestamp, days(d) or hours(h), e.g. 2006-01-02 15:04:05.99, 35d, 24h")
	cleanupAuditPtr := cleanupCommand.Bool("audit-trail", false, "delete audit trail data older than age")
	cleanupInstancePtr := cleanupCommand.Bool("workflow-instance", false, "delete workflow instance data older than age")

	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "count":
		countCommand.Parse(args)
	case "broken":
		brokenCommand.Parse(args)
	case "delete":
		deleteCommand.Parse(args)
	case "show":
		showCommand.Parse(args)
	case "restart":
		restartCommand.Parse(args)
	case "data":
		dataCommand.Parse(args)
	case "cleanup":
		cleanupCommand.Parse(args)
	default:
		printHelp()
		os.Exit(1)
	}

	conn, err = pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		exitOnErr("Unable to connect to database: %v\nMake sure that DATABASE_URL environment variable is set", err)
	}

	if countCommand.Parsed() {
		count(*countStatePtr)
	}

	if brokenCommand.Parsed() {
		broken(*brokenPatternPtr, *brokenStartTimePtr, *brokenEndTimePtr, *brokenCountTimerPtr, *brokenClassPtr)
	}

	if deleteCommand.Parsed() {
		args := deleteCommand.Args()
		delete(args)
	}

	if showCommand.Parsed() {
		args := showCommand.Args()
		show(*showDataPtr, args, *showAuditPtr, *showInstancePtr, *showDataArrPtr)
	}

	if restartCommand.Parsed() {
		args := restartCommand.Args()
		restart(args)
	}

	if dataCommand.Parsed() {
		jsonData(dataSelectorPtr, *dataStatePtr)
	}

	if cleanupCommand.Parsed() {
		cleanup(*cleanupAgePtr, *cleanupAuditPtr, *cleanupInstancePtr)
	}
}

func printHelp() {
	fmt.Println("Copper Postgres Tool\nUsage:\n",
		"count\n",
		"broken\n",
		"show\n",
		"delete\n",
		"restart\n",
		"data (only available for Json format)\n",
		"cleanup\n",
		"add -help after command to get more information")
}

func count(state string) {
	var count int64
	var err error
	stateIdx := stateIndex(state)

	if stateIdx < 0 {
		exitOnErr("Invalid state: %v. Allowed states are: %v ", state, fmt.Sprint(states))
	}

	if states[stateIdx] == "ALL" {
		err = conn.QueryRow(context.Background(), "select count(id) as WORKFLOW_COUNT from cop_workflow_instance").Scan(&count)
	} else {
		err = conn.QueryRow(context.Background(), "select count(id) as WORKFLOW_COUNT from cop_workflow_instance where state=$1;", stateIdx).Scan(&count)
	}
	if err != nil {
		exitOnErr("Error reading count: %v", err)
	}
	fmt.Fprintf(os.Stdout, "%v\n", count)
}

func broken(pattern string, stime string, etime string, count bool, classname string) {

	var sqlBuffer bytes.Buffer
	if count {
		sqlBuffer.WriteString("select count(workflow_instance_id)")
	} else {
		sqlBuffer.WriteString("select workflow_instance_id")
	}

	sqlBuffer.WriteString("from cop_workflow_instance_error as e, cop_workflow_instance as i where e.workflow_instance_id=i.id")

	if len(pattern) > 0 {
		if validLike.MatchString(pattern) {
			sqlBuffer.WriteString(" and e.exception like '%" + pattern + "%'")
		} else {
			exitOnErr("Invalid exception-pattern: %v", pattern)
		}
	}

	if len(classname) > 0 {
		if validClassName.MatchString(classname) {
			sqlBuffer.WriteString(" and i.classname='" + classname + "'")
		} else {
			exitOnErr("Invalid workflow-classname: %v", classname)
		}
	}

	if len(stime) > 0 {
		sqlBuffer.WriteString(" and e.error_ts >= '" + stime + "'")
	}
	if len(etime) > 0 {
		sqlBuffer.WriteString(" and e.error_ts <= '" + etime + "'")
	}

	sqlBuffer.WriteString(";")
	if count {
		var ids int64
		err := conn.QueryRow(context.Background(), sqlBuffer.String()).Scan(&ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Workflow instance error count failed: %v\n", err)
			return
		}
		fmt.Println(ids)
		return
	}
	rows, err := conn.Query(context.Background(), sqlBuffer.String())

	if err != nil {
		exitOnErr("Workflow instance error search failed: %v\n", err)
	}
	for rows.Next() {
		var id string
		err1 := rows.Scan(&id)
		if err1 != nil {
			fmt.Fprintf(os.Stderr, "Error reading workflow instance id, error: %v", err1)
		}
		fmt.Println(id)
	}
}

func delete(args []string) {
	var err error
	for _, id := range args {
		_, err = conn.Exec(context.Background(), "select 1 from cop_workflow_instance where id=$1 for update", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error locking workflow instance id=%v, skipping...\n", id)
			continue
		}
		_, err = conn.Exec(context.Background(), "delete from cop_response where correlation_id in (select correlation_id from cop_wait where workflow_instance_id=$1)", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleteing workflow instance from COP_RESPONSE id=%v\n", id)
			err = nil
		}
		_, err = conn.Exec(context.Background(), "delete from cop_wait where workflow_instance_id=$1", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting workflow instance from COP_WAIT id=%v\n", id)
			err = nil
		}
		_, err = conn.Exec(context.Background(), "delete from cop_workflow_instance_error where workflow_instance_id=$1", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting workflow instance from COP_WORKFLOW_INSTANCE_ERROR, id=%v\n", id)
			err = nil
		}
		_, err = conn.Exec(context.Background(), "delete from cop_workflow_instance where id=$1", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting workflow instance from COP_WORKFLOW_INSTANCE, id=%v\n", id)
		}

	}
}

func show(showData bool, args []string, audit bool, instance bool, asArray bool) {
	var data, ppoolID, classname string
	var state, priority, csWaitmode, minNumbOfResp, numbOfWaits int
	var lastModTs, creationTs time.Time
	var timeout *time.Time
	if (!showData && !audit) && !instance {
		exitOnErr("Use at least one of the following flags:[-workflow-data,-audit-trail,-instance-details]")
	}
	if (asArray && !showData) || (asArray && (audit || instance)) {
		exitOnErr("Flag -print-data-array is only allowed together with -workflow-data flag")
	}

	if asArray {
		instance = false
		audit = false
		fmt.Println("[")
	}
	for i, id := range args {
		sql := "select data, state, priority, last_mod_ts, ppool_id,cs_waitmode, min_numb_of_resp, numb_of_waits, timeout, creation_ts, classname from cop_workflow_instance where id=$1"
		err := conn.QueryRow(context.Background(), sql, id).Scan(&data, &state, &priority, &lastModTs, &ppoolID, &csWaitmode, &minNumbOfResp, &numbOfWaits, &timeout, &creationTs, &classname)
		if err == nil {
			if instance {
				stateTxt, _ := indexState(state)
				fmt.Println("Workflow Instance:")
				fmt.Printf("id:%v, state:%v, priority:%v, creation time:%v, last modification:%v\n", id, stateTxt, priority, creationTs, lastModTs)
				fmt.Printf("pool id:%v, wait mode:%v, number of waits:%v, class name:%v", ppoolID, csWaitmode, numbOfWaits, classname)
				if timeout != nil {
					fmt.Printf(", timeout:%v", *timeout)
				}
				fmt.Println("")
			}
			if showData && instance {
				fmt.Println("Instance data:")
			}
			if showData {
				fmt.Printf("%v\n", data)
			}
		}

		if audit {
			auditMsgs(id)
		}

		if i+1 < len(args) {
			if asArray {
				fmt.Println(",")
			} else {
				fmt.Println("--------------------------------------------------------------------------------------------------------------")
			}
		}
	}
	if asArray {
		fmt.Println("]")
	}
}

func auditMsgs(id string) {

	first := true
	sql := "select long_message,occurrence from cop_audit_trail_event where instance_id=$1 order by occurrence"
	rows, err := conn.Query(context.Background(), sql, id)
	if err != nil {
		return
	}

	for rows.Next() {
		if first {
			fmt.Println("Audit Trail:")
		}
		first = false
		var msg string
		var occurrence time.Time
		err1 := rows.Scan(&msg, &occurrence)

		if err1 != nil {
			fmt.Fprintf(os.Stderr, "Error retrieving audit entry:%v", err1)
			continue
		}
		dmsg, err := decodeMsg(msg)
		if err == nil {
			fmt.Printf("Occurrence: %v, message: %v\n", occurrence, dmsg)
		}
	}
}

func restart(args []string) {
	for _, id := range args {
		now := time.Now()
		sql := "insert into cop_queue (ppool_id, priority, last_mod_ts, workflow_instance_id) (select ppool_id, priority, $1, id from cop_workflow_instance where id=$2 and (state=4 or state=5))"
		_, err := conn.Exec(context.Background(), sql, now, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error restarting workflow instance: %v, %v", id, err)
			continue
		}
		sql = "update cop_workflow_instance set state=0, last_mod_ts=$1 where id=$2 and (state=4 or state=4)"
		_, err = conn.Exec(context.Background(), sql, now, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error restarting workflow instance:%v, %v", id, err)
			continue
		}
		_, err = conn.Exec(context.Background(), "delete from cop_workflow_instance_error where workflow_instance_id=$1", id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error removing workflow instance: %v from error table, %v", id, err)
		}
	}
}

func cleanup(age string, audit bool, instance bool) {

	if len(age) < 1 {
		exitOnErr("Flag -age is mandatory")
	}
	if !audit && !instance {
		exitOnErr("Use at least one of the following flags: [-audit-trail,-workflow-instance]")
	}

	atime, err := parseAge(age)
	if err != nil {
		exitOnErr("Invalid age value %v. Use valid day(d), hours(h) or timestamp format", age)
	}
	if instance {
		sql := "delete from cop_workflow_instance_error where error_ts < $1;"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_response where response_ts < $1;"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_wait where workflow_instance_id IN (select id from cop_workflow_instance where creation_ts < $1);"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_adaptercall where workflowid IN (select id from cop_workflow_instance where creation_ts < $1);"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_lock where workflow_instance_id IN (select id from cop_workflow_instance where creation_ts < $1);"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_queue where workflow_instance_id IN (select id from cop_workflow_instance where creation_ts < $1);"
		conn.Exec(context.Background(), sql, atime)
		sql = "delete from cop_workflow_instance where creation_ts < $1;"
		conn.Exec(context.Background(), sql, atime)
	}

	if audit {
		sql := "delete from cop_audit_trail_event where occurrence < $1;"
		conn.Exec(context.Background(), sql, atime)
	}
	fmt.Fprintf(os.Stdout, "deleted data older than %v", atime)
}

func readPipe() []string {
	info, _ := os.Stdin.Stat()
	if (info.Mode() & os.ModeCharDevice) == 0 {
		reader := bufio.NewReader(os.Stdin)
		var output []rune
		for {
			input, _, err := reader.ReadRune()
			if err != nil && err == io.EOF {
				break
			}
			output = append(output, input)
		}
		return strings.Split(strings.Trim(string(output), "\n"), "\n")
	}
	return nil
}

func stateIndex(state string) int {
	aState := strings.ToUpper(state)
	idx, _ := find(states, aState)
	return idx
}

func indexState(idx int) (string, error) {
	if idx > -1 && idx < len(states) {
		return states[idx], nil
	}
	return "", errors.New("Invalid state index")
}

func find(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}

func decodeMsg(msg string) (string, error) {
	var dmsg []byte
	if msg[0] != 'C' && msg[0] != 'U' {
		return msg, nil
	}
	r := base64.NewDecoder(base64.StdEncoding, strings.NewReader(msg[1:]))
	dmsg, err := ioutil.ReadAll(r)
	if err != nil {
		return msg, err
	}
	if msg[0] == 'C' {
		r, err := zlib.NewReader(bytes.NewReader(dmsg))
		if err != nil {
			return "", err
		}
		dmsg, err = ioutil.ReadAll(r)
		if err != nil {
			return "", err
		}
	}
	return string(dmsg[7:]), err
}

func jsonData(selector *string, state string) {
	var err error
	var sql string

	if selector == nil {
		exitOnErr("Flag -json-selector is mandatory")
	}

	stateIdx := stateIndex(state)
	stateInt := strconv.Itoa(stateIdx)

	if stateIdx < 0 {
		exitOnErr("Invalid state: %v. Allowed states are: %v", state, fmt.Sprint(states))
	}

	if states[stateIdx] == "ALL" {
		sql = "select id from (select id,state, data::jsonb as json from cop_workflow_instance) as r where " + *selector
	} else {
		sql = "select id from (select id,state, data::jsonb as json from cop_workflow_instance) as r where state=" + stateInt + " and " + *selector
	}

	rows, err := conn.Query(context.Background(), sql)
	if err != nil {
		exitOnErr("Query Error:%v", err)
	}
	for rows.Next() {
		var id string
		err1 := rows.Scan(&id)
		if err1 == nil {
			fmt.Println(id)
		}
	}

}

func parseAge(age string) (time.Time, error) {

	var dur string
	var err error
	var atime time.Time

	if strings.HasSuffix(age, "d") {
		days, err := strconv.Atoi(age[0 : len(age)-1])
		if err != nil {
			err = errors.New("Invalid day format " + age)
		} else {
			dur = strconv.Itoa(days*24) + "h"
		}
	} else if strings.HasSuffix(age, "h") {
		dur = age
	}

	if err == nil {
		if len(dur) > 0 {
			var value time.Duration
			value, err = time.ParseDuration(dur)
			if value <= 0 {
				err = errors.New("Invalid age value " + age)
			}
			if err == nil {
				atime = time.Now().Add(-value)
			}
		} else {
			atime, err = time.Parse(pgTimestampFormat, age)
		}

	}
	return atime, err
}

func exitOnErr(msg string, val ...interface{}) {
	if val != nil {
		fmt.Fprintf(os.Stderr, msg+"\n", val...)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
	os.Exit(1)
}
