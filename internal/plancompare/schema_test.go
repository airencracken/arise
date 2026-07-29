package plancompare

import (
	"strings"
	"testing"
)

func TestStateAndPolicySchemaContracts(t *testing.T) {
	state, err := DecodeStateDocument(strings.NewReader(`{
		"schema": 1,
		"fixture": {"schema": 1, "request": {"operation": "install", "targets": []}, "installed": [], "available": []},
		"plan": {"schema": 1, "actions": [], "decisions": {"records": [], "truncated": false, "omitted_records": 0, "encoded_bytes": 0}}
	}`))
	if err != nil || !state.Validated || !state.Valid || state.Packages == nil {
		t.Fatalf("state document = %#v, %v", state, err)
	}
	policy, err := DecodePolicyDocument(strings.NewReader(`{
		"schema": 1,
		"policy": {"required_identities": ["sys-libs/zlib:0"]}
	}`))
	if err != nil || len(policy.RequiredIdentities) != 1 {
		t.Fatalf("policy document = %#v, %v", policy, err)
	}
	for name, input := range map[string]string{
		"unknown field":  `{"schema":1,"fixture":{"schema":1,"request":{"operation":"install","targets":[]},"installed":[],"available":[]},"plan":{"schema":1,"actions":[],"decisions":{"records":[],"truncated":false,"omitted_records":0,"encoded_bytes":0}},"surprise":true}`,
		"wrong schema":   `{"schema":2,"fixture":{},"plan":{}}`,
		"trailing value": `{"schema":1,"fixture":{"schema":1,"request":{"operation":"install","targets":[]},"installed":[],"available":[]},"plan":{"schema":1,"actions":[],"decisions":{"records":[],"truncated":false,"omitted_records":0,"encoded_bytes":0}}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStateDocument(strings.NewReader(input)); err == nil {
				t.Fatal("malformed state document accepted")
			}
		})
	}
}
