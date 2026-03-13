package splitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Validate(t *testing.T) {
	testCases := []struct {
		name   string
		ref    string
		errStr string
	}{
		{name: "Empty", ref: "", errStr: "the origin is not a valid Git reference"},
		{name: "HEAD", ref: "HEAD", errStr: ""},
		{name: "HEAD^", ref: "HEAD^", errStr: "the origin is not a valid Git reference"},
		{name: "HEAD^2", ref: "HEAD^2", errStr: "the origin is not a valid Git reference"},
		{name: "HEAD-1", ref: "HEAD-1", errStr: "the origin is not a valid Git reference"},
		{name: "RandomString1", ref: "main", errStr: "the origin is not a valid Git reference"},
		{name: "RandomString2", ref: "master", errStr: "the origin is not a valid Git reference"},
		{name: "RefHeads", ref: "refs/heads/somebranch", errStr: ""},
		{name: "RefTags", ref: "refs/tags/sometag", errStr: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := &Config{
				GitVersion: "latest",
				Origin:     testCase.ref,
				Target:     testCase.ref,
			}
			err := config.Validate()
			if testCase.errStr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, testCase.errStr)
			}
		})
	}
}
