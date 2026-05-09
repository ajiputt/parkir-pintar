package money

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIDR_Format(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "Rp0"},
		{500, "Rp500"},
		{5_000, "Rp5.000"},
		{20_000, "Rp20.000"},
		{1_234_567, "Rp1.234.567"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IDR(c.in).String())
	}
}

func TestAdd_SameCurrency(t *testing.T) {
	got, err := IDR(5_000).Add(IDR(20_000))
	require.NoError(t, err)
	assert.Equal(t, int64(25_000), got.Amount())
}

func TestAdd_CurrencyMismatch(t *testing.T) {
	_, err := IDR(5_000).Add(New(1, "USD"))
	assert.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestSub_Negative(t *testing.T) {
	_, err := IDR(100).Sub(IDR(200))
	assert.ErrorIs(t, err, ErrNegative)
}

func TestMul(t *testing.T) {
	assert.Equal(t, int64(15_000), IDR(5_000).Mul(3).Amount())
}

func TestSum(t *testing.T) {
	total, err := Sum([]Money{IDR(5_000), IDR(20_000), IDR(5_000)})
	require.NoError(t, err)
	assert.Equal(t, int64(30_000), total.Amount())
}

func TestSum_Empty(t *testing.T) {
	total, err := Sum(nil)
	require.NoError(t, err)
	assert.True(t, total.IsZero())
}

func TestEquals(t *testing.T) {
	assert.True(t, IDR(5_000).Equals(IDR(5_000)))
	assert.False(t, IDR(5_000).Equals(IDR(5_001)))
	assert.False(t, IDR(5_000).Equals(New(5_000, "USD")))
}

func TestMoney_JSON_RoundTrip(t *testing.T) {
	in := IDR(30_000)
	bs, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":30000,"currency":"IDR"}`, string(bs))

	var out Money
	require.NoError(t, json.Unmarshal(bs, &out))
	assert.True(t, in.Equals(out))
}

func TestMoney_UnmarshalJSON_BareIntFallback(t *testing.T) {
	// Toleransi format lama: bare int → IDR.
	var out Money
	require.NoError(t, json.Unmarshal([]byte("30000"), &out))
	assert.Equal(t, int64(30_000), out.Amount())
	assert.Equal(t, CurrencyIDR, out.Currency())
}
