package duckdb

import "time"

// The approved pinned TIMESTAMPTZ literal parser discards fractional digits
// beyond microseconds. The child supplies its actual native cast; the parent
// accepts only the unique canonical microsecond instant within this bound.
// An absent proof is valid only when the original query is already canonical.
func workerAdmitSeriesBounds(q SeriesQuery, proof *workerSeriesBounds) (workerSeriesBounds, error) {
	if proof == nil {
		if q.From.Nanosecond()%1000 != 0 || q.To.Nanosecond()%1000 != 0 {
			return workerSeriesBounds{}, errWorkerProtocol
		}
		return workerSeriesBounds{From: q.From.UTC(), To: q.To.UTC()}, nil
	}
	admits := func(actual, requested time.Time) bool {
		delta := requested.UTC().Sub(actual)
		return actual.Location() == time.UTC && actual.Nanosecond()%1000 == 0 && delta >= 0 && delta < time.Microsecond
	}
	if !admits(proof.From, q.From) || !admits(proof.To, q.To) || proof.To.Before(proof.From) {
		return workerSeriesBounds{}, errWorkerProtocol
	}
	return *proof, nil
}
