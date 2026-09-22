package money

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Amount
		wantErr bool
	}{
		{in: "0", want: 0},
		{in: "1", want: 100},
		{in: "1.5", want: 150},
		{in: "1.50", want: 150},
		{in: "-1.23", want: -123},
		{in: "+1.23", want: 123},
		{in: " 12.34 ", want: 1234},
		{in: "31939.99", want: 3193999},
		{in: "-0.01", want: -1},
		{in: "", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1.234", wantErr: true},
		{in: "1.2.3", wantErr: true},
		// An absent part is not a zero. "." and "-" carry no digits at all, and
		// ".5"/"5." are typos, not amounts — accepting them silently stored 0
		// (or a truncated value) for input that never meant that.
		{in: ".5", wantErr: true},
		{in: "5.", wantErr: true},
		{in: ".", wantErr: true},
		{in: "-", wantErr: true},
		{in: "+", wantErr: true},
		// Exactly one sign, at the front: a second one used to be absorbed by
		// strconv.ParseInt and then negated again, so "--1" parsed as +1.00 and
		// "+-1" as -1.00.
		{in: "--1", wantErr: true},
		{in: "+-1", wantErr: true},
		{in: "-+1", wantErr: true},
		{in: "1.-5", wantErr: true},
		{in: "-1.5-", wantErr: true},
		// Out of range: whole*100 would overflow int64 (and the JSON boundary
		// cannot represent it either), so it is an error, not a wrapped value.
		{in: "1e30", wantErr: true},
		{in: "92233720368547758.08", wantErr: true},
		{in: "99999999999999999999", wantErr: true},
		// The largest amounts that still fit under MaxMinorUnits are accepted.
		{in: "46116860184273878.99", want: Amount(4611686018427387899)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFromFloatRounds(t *testing.T) {
	assert.Equal(t, Amount(123), FromFloat(1.23))
	assert.Equal(t, Amount(25050), FromFloat(250.5))
	assert.Equal(t, Amount(1), FromFloat(0.005))
	assert.Equal(t, Amount(-25050), FromFloat(-250.5))
}

func TestString(t *testing.T) {
	assert.Equal(t, "0.00", Amount(0).String())
	assert.Equal(t, "1.50", Amount(150).String())
	assert.Equal(t, "-1.23", Amount(-123).String())
	assert.Equal(t, "31939.99", Amount(3193999).String())
	// The most negative int64 still renders as a valid decimal — and therefore
	// a valid JSON number. Negating it overflows, which used to emit a
	// malformed value that made every response containing the row unmarshalable.
	assert.Equal(t, "-92233720368547758.08", Amount(math.MinInt64).String())
}

// A value at the top of the supported range must survive the float64 wire
// boundary: the bound exists precisely because float64 cannot represent every
// int64 near its maximum exactly.
func TestUnmarshalJSONRejectsNonFiniteAndOutOfRange(t *testing.T) {
	for _, body := range []string{
		`{"amount":"1e30"}`,
		`{"amount":1e30}`,
		`{"amount":"-1e30"}`,
		`{"amount":1e308}`,
		`{"amount":"NaN"}`,
		`{"amount":"Inf"}`,
		`{"amount":"-Inf"}`,
		`{"amount":"92233720368547759.99"}`,
	} {
		var p struct {
			Amount Amount `json:"amount"`
		}
		assert.Error(t, json.Unmarshal([]byte(body), &p), "payload %s", body)
	}
}

func TestUnmarshalJSONAcceptsLargeRepresentableAmount(t *testing.T) {
	// 1e15 major units (1e17 cents) is an absurd ledger balance but is exactly
	// representable as a float64, so it must still be accepted and round-trip.
	var p struct {
		Amount Amount `json:"amount"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"amount":1000000000000000.00}`), &p))
	assert.Equal(t, Amount(100000000000000000), p.Amount)

	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":1000000000000000.00}`, string(b))
}

func TestJSONRoundTrip(t *testing.T) {
	type payload struct {
		Amount Amount `json:"amount"`
	}

	var p payload
	require.NoError(t, json.Unmarshal([]byte(`{"amount":31939.99}`), &p))
	assert.Equal(t, Amount(3193999), p.Amount)

	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":31939.99}`, string(b))

	// null decodes to zero.
	var nullPayload payload
	require.NoError(t, json.Unmarshal([]byte(`{"amount":null}`), &nullPayload))
	assert.Equal(t, Amount(0), nullPayload.Amount)

	// Extra decimal places are rounded to the nearest cent.
	var rounded payload
	require.NoError(t, json.Unmarshal([]byte(`{"amount":1.239}`), &rounded))
	assert.Equal(t, Amount(124), rounded.Amount)
}

func TestScan(t *testing.T) {
	var a Amount

	require.NoError(t, a.Scan(int64(3193999)))
	assert.Equal(t, Amount(3193999), a)

	require.NoError(t, a.Scan([]byte("150")))
	assert.Equal(t, Amount(150), a)

	require.NoError(t, a.Scan("42"))
	assert.Equal(t, Amount(42), a)

	// Floats are interpreted as major units (legacy/mock rows).
	require.NoError(t, a.Scan(2.5))
	assert.Equal(t, Amount(250), a)

	require.NoError(t, a.Scan(nil))
	assert.Equal(t, Amount(0), a)

	// A float outside the supported range is refused like the JSON boundary:
	// storing it would rely on an int64 conversion that wraps.
	assert.Error(t, a.Scan(1e30))
	assert.Error(t, a.Scan(math.NaN()))
	assert.Error(t, a.Scan(math.Inf(1)))

	assert.Error(t, a.Scan(true))
}

func TestValue(t *testing.T) {
	v, err := Amount(3193999).Value()
	require.NoError(t, err)
	assert.Equal(t, int64(3193999), v)
}

func TestFloat64CentsAndAbs(t *testing.T) {
	assert.Equal(t, 1.23, Amount(123).Float64())
	assert.Equal(t, -0.01, Amount(-1).Float64())
	assert.Equal(t, int64(123), Amount(123).Cents())
	assert.Equal(t, Amount(123), Amount(-123).Abs())
	assert.Equal(t, Amount(123), Amount(123).Abs())
}

func TestScanNumericTypes(t *testing.T) {
	var a Amount

	require.NoError(t, a.Scan(int(7)))
	assert.Equal(t, Amount(7), a)

	require.NoError(t, a.Scan(int32(8)))
	assert.Equal(t, Amount(8), a)

	assert.Error(t, a.Scan([]byte("not-a-number")))
	assert.Error(t, a.Scan("not-a-number"))
}

func TestUnmarshalJSONErrors(t *testing.T) {
	var a Amount
	assert.Error(t, a.UnmarshalJSON([]byte(`"abc"`)))
	assert.Error(t, a.UnmarshalJSON([]byte(`""`)))
	assert.Error(t, a.UnmarshalJSON([]byte(`"1.2.3"`)))
}
