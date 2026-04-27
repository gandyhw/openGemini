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

package executor_test

import (
	"testing"

	"github.com/openGemini/openGemini/engine/executor"
	"github.com/openGemini/openGemini/engine/hybridqp"
	"github.com/openGemini/openGemini/lib/errno"
	"github.com/openGemini/openGemini/lib/util/lifted/influx/influxql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeRCAEventRecord(t *testing.T) {
	t.Run("alarm decodes timestamps", func(t *testing.T) {
		record, err := executor.DecodeRCAEventRecord(
			"alarm-001",
			"entity-a",
			executor.ALARM,
			`{"start_time":1000,"end_time":2000,"create_time":900}`,
		)
		require.NoError(t, err)

		assert.Equal(t, "alarm-001", record.ID)
		assert.Equal(t, "entity-a", record.EntityID)
		assert.Equal(t, executor.ALARM, record.Type)
		assert.Equal(t, int64(1000), record.StartTS)
		assert.True(t, record.HasStartTS)
		assert.Equal(t, int64(2000), record.EndTS)
		assert.True(t, record.HasEndTS)
		assert.Equal(t, int64(900), record.CreatedTS)
		assert.True(t, record.HasCreatedTS)
	})

	t.Run("event decodes create time without start or end", func(t *testing.T) {
		record, err := executor.DecodeRCAEventRecord(
			"event-001",
			"entity-b",
			executor.EVENT,
			`{"create_time":3000}`,
		)
		require.NoError(t, err)

		assert.Equal(t, int64(3000), record.CreatedTS)
		assert.True(t, record.HasCreatedTS)
		assert.False(t, record.HasStartTS)
		assert.False(t, record.HasEndTS)
	})

	t.Run("event decodes start and end timestamps", func(t *testing.T) {
		record, err := executor.DecodeRCAEventRecord(
			"event-002",
			"entity-b",
			executor.EVENT,
			`{"start_time":2000,"end_time":2500,"create_time":1000}`,
		)
		require.NoError(t, err)

		assert.Equal(t, int64(2000), record.StartTS)
		assert.True(t, record.HasStartTS)
		assert.Equal(t, int64(2500), record.EndTS)
		assert.True(t, record.HasEndTS)
		assert.Equal(t, int64(1000), record.CreatedTS)
		assert.True(t, record.HasCreatedTS)
	})

	t.Run("anomaly decodes timestamp arrays", func(t *testing.T) {
		record, err := executor.DecodeRCAEventRecord(
			"anomaly-001",
			"entity-c",
			executor.ANOMALY,
			`{"timestamps":[4000,5000,6000]}`,
		)
		require.NoError(t, err)

		assert.Equal(t, []int64{4000, 5000, 6000}, record.Timestamps)
	})
}

func TestDecodeRCAEventRecordSchemaErrors(t *testing.T) {
	tests := []struct {
		name        string
		eventType   string
		annotations string
	}{
		{
			name:        "alarm missing start time",
			eventType:   executor.ALARM,
			annotations: `{"create_time":1000}`,
		},
		{
			name:        "alarm missing create time",
			eventType:   executor.ALARM,
			annotations: `{"start_time":1000}`,
		},
		{
			name:        "event missing create time",
			eventType:   executor.EVENT,
			annotations: `{"start_time":1000}`,
		},
		{
			name:        "anomaly missing timestamps",
			eventType:   executor.ANOMALY,
			annotations: `{"description":"missing timestamps"}`,
		},
		{
			name:        "malformed json",
			eventType:   executor.EVENT,
			annotations: `{"create_time":`,
		},
		{
			name:        "timestamp is wrong type",
			eventType:   executor.ALARM,
			annotations: `{"start_time":"bad"}`,
		},
		{
			name:        "timestamp array contains wrong type",
			eventType:   executor.ANOMALY,
			annotations: `{"timestamps":[1000,"bad"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executor.DecodeRCAEventRecord("id", "entity", tt.eventType, tt.annotations)
			require.Error(t, err)
			assert.True(t, errno.Equal(err, errno.RCAEventSchemaInvalid), err.Error())
		})
	}
}

func TestBuildRCAEventRecords(t *testing.T) {
	chunk := newRCAEventTestChunk(t,
		[]string{"anomaly-001", "alarm-001", "event-001"},
		[]string{"entity-a", "entity-b", "entity-c"},
		[]string{executor.ANOMALY, executor.ALARM, executor.EVENT},
		[]string{
			`{"timestamps":[1000,2000]}`,
			`{"start_time":3000,"end_time":4000,"create_time":2500}`,
			`{"start_time":5000,"create_time":6000}`,
		},
	)

	records, err := executor.BuildRCAEventRecords([]executor.Chunk{chunk}, rcaEventTestColMap())
	require.NoError(t, err)
	require.Len(t, records, 3)

	assert.Equal(t, []int64{1000, 2000}, records[0].Timestamps)
	assert.Equal(t, int64(3000), records[1].StartTS)
	assert.True(t, records[1].HasEndTS)
	assert.Equal(t, int64(6000), records[2].CreatedTS)
}

func newRCAEventTestChunk(t *testing.T, ids, entityIDs, types, annotations []string) executor.Chunk {
	t.Helper()
	require.Len(t, entityIDs, len(ids))
	require.Len(t, types, len(ids))
	require.Len(t, annotations, len(ids))

	rowDataType := hybridqp.NewRowDataTypeImpl(
		influxql.VarRef{Val: executor.ID, Type: influxql.String},
		influxql.VarRef{Val: executor.EntityID, Type: influxql.String},
		influxql.VarRef{Val: executor.Type, Type: influxql.String},
		influxql.VarRef{Val: executor.Annotations, Type: influxql.String},
	)
	chunk := executor.NewChunkBuilder(rowDataType).NewChunk("rca_event_test")
	times := make([]int64, len(ids))
	for i := range times {
		times[i] = int64(i + 1)
	}
	chunk.AppendTimes(times)

	values := [][]string{ids, entityIDs, types, annotations}
	for i, columnValues := range values {
		chunk.Column(i).AppendStringValues(columnValues)
		chunk.Column(i).AppendColumnTimes(times)
		chunk.Column(i).AppendManyNotNil(len(columnValues))
	}
	return chunk
}

func rcaEventTestColMap() map[string]int {
	return map[string]int{
		executor.ID:          0,
		executor.EntityID:    1,
		executor.Type:        2,
		executor.Annotations: 3,
	}
}
