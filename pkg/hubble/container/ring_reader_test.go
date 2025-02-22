// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Authors of Hubble

// +build !privileged_tests

package container

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	v1 "github.com/cilium/cilium/pkg/hubble/api/v1"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRingReader_Previous(t *testing.T) {
	ring := NewRing(Capacity15)
	for i := 0; i < 15; i++ {
		ring.Write(&v1.Event{Timestamp: &timestamppb.Timestamp{Seconds: int64(i)}})
	}
	tests := []struct {
		start   uint64
		count   int
		want    []*v1.Event
		wantErr error
	}{
		{
			start: 13,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 13}},
			},
		}, {
			start: 13,
			count: 2,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 13}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 12}},
			},
		}, {
			start: 5,
			count: 5,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 5}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 4}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 3}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 2}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 1}},
			},
		}, {
			start: 0,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
			},
		}, {
			start: 0,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
			},
		}, {
			start:   14,
			count:   1,
			wantErr: io.EOF,
		},
		{
			start:   ^uint64(0),
			count:   1,
			wantErr: ErrInvalidRead,
		},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("read %d, start at position %d", tt.count, tt.start)
		t.Run(name, func(t *testing.T) {
			reader := NewRingReader(ring, tt.start)
			var got []*v1.Event
			for i := 0; i < tt.count; i++ {
				event, err := reader.Previous()
				if err != tt.wantErr {
					t.Errorf(`"%s" error = %v, wantErr %v`, name, err, tt.wantErr)
				}
				if err != nil {
					return
				}
				got = append(got, event)
			}
			assert.Equal(t, tt.want, got)
			assert.Nil(t, reader.Close())
		})
	}
}

func TestRingReader_Next(t *testing.T) {
	ring := NewRing(Capacity15)
	for i := 0; i < 15; i++ {
		ring.Write(&v1.Event{Timestamp: &timestamppb.Timestamp{Seconds: int64(i)}})
	}

	tests := []struct {
		start   uint64
		count   int
		want    []*v1.Event
		wantErr error
	}{
		{
			start: 0,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
			},
		}, {
			start: 0,
			count: 2,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 1}},
			},
		}, {
			start: 5,
			count: 5,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 5}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 6}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 7}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 8}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 9}},
			},
		}, {
			start: 13,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 13}},
			},
		}, {
			start:   ^uint64(0),
			count:   1,
			wantErr: ErrInvalidRead,
		}, {
			start:   14,
			count:   1,
			wantErr: io.EOF,
		},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("read %d, start at position %d", tt.count, tt.start)
		t.Run(name, func(t *testing.T) {
			reader := NewRingReader(ring, tt.start)
			var got []*v1.Event
			for i := 0; i < tt.count; i++ {
				event, err := reader.Next()
				if err != tt.wantErr {
					t.Errorf(`"%s" error = %v, wantErr %v`, name, err, tt.wantErr)
				}
				if err != nil {
					return
				}
				got = append(got, event)
			}
			assert.Equal(t, tt.want, got)
			assert.Nil(t, reader.Close())
		})
	}
}

func TestRingReader_NextFollow(t *testing.T) {
	defer goleak.VerifyNone(
		t,
		// ignore go routines started by the redirect we do from klog to logrus
		goleak.IgnoreTopFunction("k8s.io/klog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("io.(*pipe).Read"))
	ring := NewRing(Capacity15)
	for i := 0; i < 15; i++ {
		ring.Write(&v1.Event{Timestamp: &timestamppb.Timestamp{Seconds: int64(i)}})
	}

	tests := []struct {
		start       uint64
		count       int
		want        []*v1.Event
		wantTimeout bool
	}{
		{
			start: 0,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
			},
		}, {
			start: 0,
			count: 2,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 0}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 1}},
			},
		}, {
			start: 5,
			count: 5,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 5}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 6}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 7}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 8}},
				{Timestamp: &timestamppb.Timestamp{Seconds: 9}},
			},
		}, {
			start: 13,
			count: 1,
			want: []*v1.Event{
				{Timestamp: &timestamppb.Timestamp{Seconds: 13}},
			},
		}, {
			start:       14,
			count:       1,
			want:        []*v1.Event{nil},
			wantTimeout: true,
		},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("read %d, start at position %d, expect timeout=%t", tt.count, tt.start, tt.wantTimeout)
		t.Run(name, func(t *testing.T) {
			reader := NewRingReader(ring, tt.start)
			var timedOut bool
			var got []*v1.Event
			for i := 0; i < tt.count; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				got = append(got, reader.NextFollow(ctx))
				select {
				case <-ctx.Done():
					timedOut = true
				default:
					assert.NotNil(t, got[i])
				}
				cancel()
				assert.Nil(t, reader.Close())
			}
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantTimeout, timedOut)
		})
	}
}

func TestRingReader_NextFollow_WithEmptyRing(t *testing.T) {
	defer goleak.VerifyNone(
		t,
		// ignore go routines started by the redirect we do from klog to logrus
		goleak.IgnoreTopFunction("k8s.io/klog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("io.(*pipe).Read"))
	ring := NewRing(Capacity15)
	reader := NewRingReader(ring, ring.LastWriteParallel())
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan *v1.Event)
	go func() {
		select {
		case <-ctx.Done():
		case c <- reader.NextFollow(ctx):
		}
	}()
	select {
	case <-c:
		t.Fail()
	case <-time.After(100 * time.Millisecond):
		// the call blocked, we're good
	}
	cancel()
	assert.Nil(t, reader.Close())
}
