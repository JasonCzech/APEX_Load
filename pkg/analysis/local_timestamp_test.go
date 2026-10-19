package analysis

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestResolveLocalTimestamp(t *testing.T) {
	for _, tc := range []struct {
		zone, wall, utc string
		state           LocalTimestampResolution
	}{
		{"America/New_York", "2026-03-08T01:30:00", "2026-03-08T06:30:00Z", LocalTimestampUnique},
		{"America/New_York", "2026-03-08T02:30:00", "", LocalTimestampNonexistent},
		{"America/New_York", "2026-03-08T03:30:00", "2026-03-08T07:30:00Z", LocalTimestampUnique},
		{"America/New_York", "2026-11-01T01:30:00", "", LocalTimestampAmbiguous},
		{"America/New_York", "2026-11-01T02:00:00", "2026-11-01T07:00:00Z", LocalTimestampUnique},
		{"Australia/Lord_Howe", "2026-04-05T01:45:00", "", LocalTimestampAmbiguous},
		{"Australia/Lord_Howe", "2026-10-04T02:15:00", "", LocalTimestampNonexistent},
		{"Pacific/Apia", "2011-12-30T12:00:00", "", LocalTimestampNonexistent},
		{"Pacific/Kwajalein", "1969-09-30T12:00:00", "", LocalTimestampAmbiguous},
		{"UTC", "0001-01-01T00:00:00", "0001-01-01T00:00:00Z", LocalTimestampUnique},
		{"Asia/Kathmandu", "2026-01-01T12:00:00.123456", "2026-01-01T06:15:00.123456Z", LocalTimestampUnique},
	} {
		t.Run(tc.zone+tc.wall, func(t *testing.T) {
			wall, err := time.Parse("2006-01-02T15:04:05", tc.wall)
			if err != nil {
				t.Fatal(err)
			}
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			got, state := ResolveLocalTimestamp(wall, loc)
			if state != tc.state {
				t.Fatalf("state %s, want %s", state, tc.state)
			}
			if state == LocalTimestampUnique && got.Format(time.RFC3339Nano) != tc.utc {
				t.Fatalf("instant %s want %s", got, tc.utc)
			}
			if state != LocalTimestampUnique && !got.IsZero() {
				t.Fatal("unqualified input returned an instant")
			}
		})
	}
}

func TestResolveLocalTimestampLimits(t *testing.T) {
	for _, tc := range []struct {
		wall time.Time
		loc  *time.Location
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil},
		{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.FixedZone("outside limit", 86401)},
	} {
		if _, state := ResolveLocalTimestamp(tc.wall, tc.loc); state != LocalTimestampUnqualified {
			t.Fatalf("state %s", state)
		}
	}
}

func TestResolveLocalTimestampBoundsTransitionTraversal(t *testing.T) {
	wall := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	var b bytes.Buffer
	b.WriteString("TZif")
	b.Write(make([]byte, 16))
	for _, n := range []uint32{0, 0, 0, 64, 2, 4} {
		if err := binary.Write(&b, binary.BigEndian, n); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 64; i++ {
		if err := binary.Write(&b, binary.BigEndian, int32(wall.Add(time.Duration(i-47)*time.Hour).Unix())); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 64; i++ {
		b.WriteByte(byte(i % 2))
	}
	for i := 0; i < 2; i++ {
		if err := binary.Write(&b, binary.BigEndian, int32(0)); err != nil {
			t.Fatal(err)
		}
		b.WriteByte(0)
		b.WriteByte(byte(i * 2))
	}
	b.WriteString("X\x00Y\x00")
	loc, err := time.LoadLocationFromTZData("bounded-test", b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, state := ResolveLocalTimestamp(wall, loc); state != LocalTimestampUnqualified {
		t.Fatalf("exhausted traversal classified as %s", state)
	}
}
