package money

import (
	"encoding/json"
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
		{in: ".5", want: 50},
		{in: "-1.23", want: -123},
		{in: "+1.23", want: 123},
		{in: " 12.34 ", want: 1234},
		{in: "31939.99", want: 3193999},
		{in: "-0.01", want: -1},
		{in: "", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1.234", wantErr: true},
		{in: "1.2.3", wantErr: true},
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

	assert.Error(t, a.Scan(true))
}

func TestValue(t *testing.T) {
	v, err := Amount(3193999).Value()
	require.NoError(t, err)
	assert.Equal(t, int64(3193999), v)
}
