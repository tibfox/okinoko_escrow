package contract_test

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"vsc-node/lib/test_utils"
	"vsc-node/modules/db/vsc/contracts"
	ledgerDb "vsc-node/modules/db/vsc/ledger"
	stateEngine "vsc-node/modules/state-processing"

	"github.com/stretchr/testify/assert"
)

var _ = embed.FS{} // just so "embed" can be imported

const ContractID = "vsctestcontract"
const ownerAddress = "hive:tibfox"

//go:embed artifacts/main.wasm
var ContractWasm []byte

// Setup an Instance of a test
func SetupContractTest() *test_utils.ContractTest {
	CleanBadgerDB()
	ct := test_utils.NewContractTest()
	ct.RegisterContract(ContractID, ownerAddress, ContractWasm)
	ct.Deposit("hive:sender", 1000, ledgerDb.AssetHive)
	ct.Deposit("hive:sender", 1000, ledgerDb.AssetHbd)
	return &ct
}
func PrintBalances(ct *test_utils.ContractTest, addresses []string) {
	bal := ct.GetBalance("hive:sender", ledgerDb.AssetHive)
	fmt.Printf("sender: %d %s\n", bal, ledgerDb.AssetHive)
	bal = ct.GetBalance("hive:receiver", ledgerDb.AssetHive)
	fmt.Printf("receiver: %d %s\n", bal, ledgerDb.AssetHive)
}

// clean the db for multiple (sequential) tests
func CleanBadgerDB() {
	err := os.RemoveAll("data/badger")
	if err != nil {
		panic("failed to remove data/badger")
	}
}

// CallContract executes a contract action and asserts basic success
func CallContract(t *testing.T, ct *test_utils.ContractTest, action string, payload json.RawMessage, intents []contracts.Intent, authUser string, expectedResult bool, maxGas uint) (stateEngine.TxResult, uint, map[string][]string) {
	fmt.Println(action)
	result, gasUsed, logs := ct.Call(stateEngine.TxVscCallContract{
		Caller: authUser,

		Self: stateEngine.TxSelf{
			TxId:                 fmt.Sprintf("%s-tx", action),
			BlockId:              "block1",
			Index:                0,
			OpIndex:              0,
			Timestamp:            "2025-09-03T00:00:00",
			RequiredAuths:        []string{authUser},
			RequiredPostingAuths: []string{},
		},
		ContractId: ContractID,
		Action:     action,
		Payload:    payload,
		RcLimit:    1000,
		Intents:    intents,
	})

	// PrintLogs(logs)
	// PrintErrorIfFailed(result)

	fmt.Printf("gas used: %d\n", gasUsed)
	fmt.Printf("gas max : %d\n", maxGas)
	fmt.Printf("return msg: %s\n", result.Ret)
	assert.LessOrEqual(t, gasUsed, maxGas, fmt.Sprintf("Gas %d exceeded limit %d", gasUsed, maxGas))

	if expectedResult {
		assert.True(t, result.Success, "Contract action failed with "+result.Ret)
	} else {
		assert.False(t, result.Success, "Contract action did not fail (as expected)")
	}
	return result, gasUsed, logs
}

// PrintLogs prints all logs from a contract call
func PrintLogs(logs map[string][]string) {
	for key, values := range logs {
		for _, v := range values {
			fmt.Printf("[%s] %s\n", key, v)
		}
	}
}

// PrintErrorIfFailed prints error if the contract call failed
func PrintErrorIfFailed(result stateEngine.TxResult) {
	if !result.Success {
		fmt.Println(result.Err)
	}
}

// ToJSONRaw converts Go objects to json.RawMessage
func ToJSONRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return b
}

// PayloadToJSON safely converts payloads to json.RawMessage
func PayloadToJSON(v any) json.RawMessage {
	switch val := v.(type) {
	case string:
		return json.RawMessage([]byte(val)) // no quoting
	case json.RawMessage:
		return val
	default:
		return ToJSONRaw(val) // fallback to normal marshaling
	}
}

type ContractTestCase struct {
	Name          string
	Action        string
	Payload       any
	Intents       []contracts.Intent
	ExpectLogs    bool
	AuthUser      string
	ExpectSuccess bool
}

// for table-driven tests
func RunContractTests(t *testing.T, ct *test_utils.ContractTest, tests []ContractTestCase) {
	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.Name, func(t *testing.T) {
			CallContract(t, ct, tt.Action, PayloadToJSON(tt.Payload), tt.Intents, tt.AuthUser, tt.ExpectSuccess, uint(100_000_000))
		})
	}
}
