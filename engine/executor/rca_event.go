// Copyright 2026 Huawei Cloud Computing Technologies Co., Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/openGemini/openGemini/lib/errno"
)

type RCAEventRecord struct {
	ID       string
	EntityID string
	Type     string

	Timestamps []int64

	StartTS      int64
	HasStartTS   bool
	EndTS        int64
	HasEndTS     bool
	CreatedTS    int64
	HasCreatedTS bool
}

func DecodeRCAEventRecord(id, entityID, eventType, annotations string) (RCAEventRecord, error) {
	record := RCAEventRecord{
		ID:       id,
		EntityID: entityID,
		Type:     eventType,
	}

	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(annotations), &fields); err != nil {
		return RCAEventRecord{}, rcaEventSchemaError("%s annotations for %s are not valid JSON: %v", eventType, id, err)
	}

	var err error
	switch eventType {
	case ANOMALY:
		record.Timestamps, err = decodeRequiredInt64Array(fields, Timestamps, id, eventType)
	case ALARM:
		record.StartTS, err = decodeRequiredInt64(fields, StartTS, id, eventType)
		if err == nil {
			record.HasStartTS = true
			record.EndTS, record.HasEndTS, err = decodeOptionalInt64(fields, EndTS, id, eventType, true)
		}
		if err == nil {
			record.CreatedTS, err = decodeRequiredInt64(fields, CreatedTS, id, eventType)
		}
		if err == nil {
			record.HasCreatedTS = true
		}
	case EVENT:
		record.CreatedTS, err = decodeRequiredInt64(fields, CreatedTS, id, eventType)
		if err == nil {
			record.HasCreatedTS = true
			record.StartTS, record.HasStartTS, err = decodeOptionalInt64(fields, StartTS, id, eventType, false)
		}
		if err == nil {
			record.EndTS, record.HasEndTS, err = decodeOptionalInt64(fields, EndTS, id, eventType, false)
		}
	}
	if err != nil {
		return RCAEventRecord{}, err
	}
	return record, nil
}

func BuildRCAEventRecords(chunks []Chunk, colMap map[string]int) ([]RCAEventRecord, error) {
	records := make([]RCAEventRecord, 0)
	for _, chunk := range chunks {
		columns := chunk.Columns()
		idCol := columns[colMap[ID]]
		entityIDCol := columns[colMap[EntityID]]
		typeCol := columns[colMap[Type]]
		annotationsCol := columns[colMap[Annotations]]

		for i := 0; i < idCol.Length(); i++ {
			record, err := DecodeRCAEventRecord(
				idCol.StringValue(i),
				entityIDCol.StringValue(i),
				typeCol.StringValue(i),
				annotationsCol.StringValue(i),
			)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	}
	return records, nil
}

func decodeRequiredInt64(fields map[string]json.RawMessage, field, id, eventType string) (int64, error) {
	value, ok := fields[field]
	if !ok {
		return 0, rcaEventSchemaError("%s annotations for %s are missing required field %s", eventType, id, field)
	}
	ts, _, err := decodeInt64Raw(value, field, id, eventType, false)
	return ts, err
}

func decodeOptionalInt64(
	fields map[string]json.RawMessage,
	field, id, eventType string,
	emptyStringMeansPresent bool,
) (int64, bool, error) {
	value, ok := fields[field]
	if !ok {
		return 0, false, nil
	}
	ts, present, err := decodeInt64Raw(value, field, id, eventType, emptyStringMeansPresent)
	return ts, present, err
}

func decodeInt64Raw(
	value json.RawMessage,
	field, id, eventType string,
	emptyStringMeansPresent bool,
) (int64, bool, error) {
	if emptyStringMeansPresent {
		var text string
		if err := json.Unmarshal(value, &text); err == nil && text == "" {
			return 0, true, nil
		}
	}

	dec := json.NewDecoder(bytes.NewReader(value))
	dec.UseNumber()
	var number json.Number
	if err := dec.Decode(&number); err != nil {
		return 0, false, rcaEventSchemaError("%s annotations for %s field %s must be a number", eventType, id, field)
	}
	ts, err := jsonNumberToInt64(number)
	if err != nil {
		return 0, false, rcaEventSchemaError("%s annotations for %s field %s must be a valid timestamp: %v", eventType, id, field, err)
	}
	return ts, true, nil
}

func decodeRequiredInt64Array(fields map[string]json.RawMessage, field, id, eventType string) ([]int64, error) {
	value, ok := fields[field]
	if !ok {
		return nil, rcaEventSchemaError("%s annotations for %s are missing required field %s", eventType, id, field)
	}

	dec := json.NewDecoder(bytes.NewReader(value))
	dec.UseNumber()
	var values []json.Number
	if err := dec.Decode(&values); err != nil {
		return nil, rcaEventSchemaError("%s annotations for %s field %s must be a number array", eventType, id, field)
	}
	timestamps := make([]int64, 0, len(values))
	for _, value := range values {
		ts, err := jsonNumberToInt64(value)
		if err != nil {
			return nil, rcaEventSchemaError("%s annotations for %s field %s must contain valid timestamps: %v", eventType, id, field, err)
		}
		timestamps = append(timestamps, ts)
	}
	return timestamps, nil
}

func jsonNumberToInt64(number json.Number) (int64, error) {
	ts, err := number.Int64()
	if err == nil {
		return ts, nil
	}
	floatValue, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return 0, err
	}
	return int64(floatValue), nil
}

func rcaEventSchemaError(format string, args ...interface{}) error {
	return errno.NewError(errno.RCAEventSchemaInvalid, fmt.Sprintf(format, args...))
}
