package toolpolicy

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideMatrix pins every cell of the permission matrix from the spec.
func TestDecideMatrix(t *testing.T) {
	want := map[Class]map[Profile]Outcome{
		ClassRead:          {ProfileRead: AllowReal, ProfilePaper: AllowReal, ProfileLive: AllowReal},
		ClassTradeIncrease: {ProfileRead: Deny, ProfilePaper: AllowPaper, ProfileLive: AllowReal},
		ClassTradeReduce:   {ProfileRead: Deny, ProfilePaper: AllowPaper, ProfileLive: AllowReal},
		ClassControl:       {ProfileRead: Deny, ProfilePaper: Deny, ProfileLive: AllowReal},
		ClassSafety:        {ProfileRead: Deny, ProfilePaper: AllowReal, ProfileLive: AllowReal},
	}
	require.Len(t, want, len(Classes))
	for c, row := range want {
		for p, outcome := range row {
			assert.Equal(t, outcome, Decide(p, c), "class=%s profile=%s", c, p)
		}
	}
}

func TestDecideUnknownIsDenied(t *testing.T) {
	assert.Equal(t, Deny, Decide(ProfileLive, Class("")))
	assert.Equal(t, Deny, Decide(ProfileLive, Class("bogus")))
	assert.Equal(t, Deny, Decide(Profile(""), ClassRead))
	assert.Equal(t, Deny, Decide(Profile("admin"), ClassRead))
}

func TestProfileFromClaim(t *testing.T) {
	cases := []struct {
		claim string
		level int
		want  Profile
	}{
		{"", 1, ProfileRead},
		{"", 2, ProfileRead},
		{"", 3, ProfileRead},
		{"", 4, ProfileLive},
		{"", 9, ProfileLive},
		{"read", 4, ProfileRead},
		{"paper", 4, ProfilePaper},
		{"live", 1, ProfileLive},
		{"admin", 4, ProfileRead},
		{"LIVE", 4, ProfileRead},
		{" live", 4, ProfileRead},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, ProfileFromClaim(tc.claim, tc.level), "claim=%q level=%d", tc.claim, tc.level)
	}
}

func TestClassForCallArguments(t *testing.T) {
	tool := "submit_futures_order_direct"
	c, ok := ClassForCall(tool, nil)
	require.True(t, ok)
	assert.Equal(t, ClassTradeIncrease, c)

	for _, k := range []string{"reduce_only", "close_order"} {
		c, _ = ClassForCall(tool, map[string]interface{}{k: true})
		assert.Equal(t, ClassTradeReduce, c, k)
		c, _ = ClassForCall(tool, map[string]interface{}{k: false})
		assert.Equal(t, ClassTradeIncrease, c, k)
		// Ambiguous values are not trusted.
		c, _ = ClassForCall(tool, map[string]interface{}{k: "true"})
		assert.Equal(t, ClassTradeIncrease, c, k)
		c, _ = ClassForCall(tool, map[string]interface{}{k: 1.0})
		assert.Equal(t, ClassTradeIncrease, c, k)
	}

	// Other order tools ignore the flag (their class is fixed).
	c, _ = ClassForCall("submit_order", map[string]interface{}{"reduce_only": true})
	assert.Equal(t, ClassTradeIncrease, c)
}

func TestUnclassifiedTool(t *testing.T) {
	_, ok := ClassOf("definitely_not_a_tool")
	assert.False(t, ok)
	_, ok = ClassForCall("definitely_not_a_tool", nil)
	assert.False(t, ok)
}

func TestTableIntegrity(t *testing.T) {
	valid := map[Class]bool{}
	for _, c := range Classes {
		valid[c] = true
	}
	names := make([]string, 0, len(table))
	for _, en := range table {
		assert.True(t, valid[en.Class], "%s has invalid class %q", en.Name, en.Class)
		names = append(names, en.Name)
	}
	assert.True(t, sort.StringsAreSorted(names), "table must stay sorted by name")

	// Arg classifiers only make sense on trade_increase tools.
	for tool := range argClassifiers {
		c, ok := ClassOf(tool)
		require.True(t, ok, tool)
		assert.Equal(t, ClassTradeIncrease, c, tool)
	}
}

// Every classified tool declares a data origin, and only classified tools do.
func TestOriginTableMatchesClassTable(t *testing.T) {
	assert.Equal(t, Names(), OriginNames())
	valid := map[Origin]bool{}
	for _, o := range Origins {
		valid[o] = true
	}
	for _, n := range OriginNames() {
		o, ok := OriginOf(n)
		require.True(t, ok, n)
		assert.True(t, valid[o], "tool %s has invalid origin %q", n, o)
	}
}
