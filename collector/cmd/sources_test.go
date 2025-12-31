package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSourceDefaults(t *testing.T) {
	src, err := resolveSource("")
	require.NoError(t, err)
	require.Equal(t, defaultSourceID, src.ID())
}

func TestResolveSourceUnknown(t *testing.T) {
	_, err := resolveSource("unknown-source")
	require.Error(t, err)
}

func TestVicSourceRegistered(t *testing.T) {
	src, err := resolveSource("vic")
	require.NoError(t, err)
	require.Equal(t, "vic", src.ID())
	// Do not invoke Run in unit tests to avoid hitting the live site.
}

func TestCanonicalSourceID_Synonyms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: "vic", want: "vic"},
		{in: "Victoria", want: "vic"},
		{in: "Victorian Government", want: "vic"},
		{in: "western australia", want: "wa"},
		{in: "Western Australian Government", want: "wa"},
		{in: "NSW", want: "nsw"},
		{in: "New South Wales", want: "nsw"},
		{in: "Federal Government", want: "federal"},
		{in: "Commonwealth", want: "federal"},
		{in: "Austender", want: "federal"},
		{in: "", want: "federal"},
		{in: "qld", want: "qld"},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, CanonicalSourceID(tc.in), "input=%q", tc.in)
	}
}

func TestDetectSourceFromText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: "Show spend for Victoria suppliers", want: "vic"},
		{in: "Compare Western Australia to NSW", want: "wa"},
		{in: "Federal government contract spend", want: "federal"},
		{in: "Austender awards in 2024", want: "federal"},
		{in: "No jurisdiction mentioned", want: ""},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, DetectSourceFromText(tc.in), "input=%q", tc.in)
	}
}

func TestDetectSourceFromTextWithEvidence(t *testing.T) {
	t.Parallel()

	source, evidence := DetectSourceFromTextWithEvidence("Compare spend in Western Australia")
	require.Equal(t, "wa", source)
	require.Equal(t, "phrase matched: western australia", evidence)

	source, evidence = DetectSourceFromTextWithEvidence("VIC procurement spend")
	require.Equal(t, "vic", source)
	require.Equal(t, "token matched: vic", evidence)

	source, evidence = DetectSourceFromTextWithEvidence("No jurisdiction here")
	require.Equal(t, "", source)
	require.Equal(t, "no match", evidence)
}
