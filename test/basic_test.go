package contract_test

import (
	"testing"
	"vsc-node/modules/db/vsc/contracts"
	ledgerDb "vsc-node/modules/db/vsc/ledger"

	"github.com/stretchr/testify/assert"
)

// collection tests

func TestEscrowCreate(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))
	PrintBalances(ct, []string{"hive:sender", "hive:receiver"})
	bal := ct.GetBalance("hive:sender", ledgerDb.AssetHive)
	assert.Equal(t, int64(0), bal)
	CallContract(t, ct, "e_get", []byte("0"), nil, "hive:sender", true, uint(100_000_000))
}

// general creation of an escrow without enough funds
func TestEscrowCreateNotEnoughFunds(t *testing.T) {
	ct := SetupContractTest()
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.001", "token": "hive"}}}, "hive:sender", false, uint(100_000_000))
}

// general creation of an escrow without - intent
func TestEscrowCreateZeroIntent(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "-1.000", "token": "hbd"}}}, "hive:sender", false, uint(100_000_000))
}

// general creation of an escrow setting self as receiver
func TestEscrowCreateSelfAsReceiver(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:sender|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.001", "token": "hbd"}}}, "hive:sender", false, uint(100_000_000))
}

// add decision without being an escrow party
func TestEscrowDecisionNotPartyMember(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:notmember", false, uint(100_000_000))
}

// add decision with invalid outcome parameter
func TestEscrowDecisionWrongParameter(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|asd"),
		nil, "hive:notmreceiverember", false, uint(100_000_000))
}

// add decision without decision parameter
func TestEscrowDecisionWrongPayload1(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|"),
		nil, "hive:receiver", false, uint(100_000_000))
}

// add decision without escrow parameter
func TestEscrowDecisionWrongPayload2(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("|f"),
		nil, "hive:receiver", false, uint(100_000_000))
}

// add decision to unknown escrow
func TestEscrowDecisionEscrowNotFound(t *testing.T) {
	ct := SetupContractTest()
	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:notmember", false, uint(100_000_000))
}

// get unknown escrow
func TestEscrowGetEscrowNotFound(t *testing.T) {
	ct := SetupContractTest()
	// decision on escrow by sender
	CallContract(t, ct, "e_get",
		[]byte("123"),
		nil, "hive:sender", false, uint(100_000_000))
}

// sender and receiver agree to RELEASE
func TestEscrowDecisionAgreeRelease(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:sender", true, uint(100_000_000))

	// decision on escrow by receiver
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:receiver", true, uint(100_000_000))
	// PrintBalances(ct, []string{"hive:sender", "hive:receiver"})
	bal := ct.GetBalance("hive:receiver", ledgerDb.AssetHive)
	assert.Equal(t, int64(1000), bal)
	// CallContract(t, ct, "e_get", []byte("0"), nil, "hive:sender", true, uint(100_000_000))
}

// sender and receiver disagree / arb agrees ro RELEASE
func TestEscrowDecisionDisAgreeRelease(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|f"),
		nil, "hive:sender", true, uint(100_000_000))

	// decision on escrow by receiver
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:receiver", true, uint(100_000_000))

	// decision on escrow by arbitrator
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:arbitrator", true, uint(100_000_000))
	// PrintBalances(ct, []string{"hive:sender", "hive:receiver"})
	bal := ct.GetBalance("hive:receiver", ledgerDb.AssetHive)
	assert.Equal(t, int64(1000), bal)
}

// sender and receiver agree to REFUND
func TestEscrowDecisionAgreeRefund(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|f"),
		nil, "hive:sender", true, uint(100_000_000))

	// decision on escrow by receiver
	CallContract(t, ct, "e_decide",
		[]byte("0|f"),
		nil, "hive:receiver", true, uint(100_000_000))
	// PrintBalances(ct, []string{"hive:sender", "hive:receiver"})
	bal := ct.GetBalance("hive:sender", ledgerDb.AssetHive)
	assert.Equal(t, int64(1000), bal)
}

// sender and receiver disagree / arb agrees ro REFUND
func TestEscrowDecisionDisAgreeRefund(t *testing.T) {
	ct := SetupContractTest()
	// just create an escrow
	CallContract(t, ct, "e_create",
		[]byte("escrow name|hive:receiver|hive:arbitrator"),
		[]contracts.Intent{{Type: "transfer.allow", Args: map[string]string{"limit": "1.000", "token": "hive"}}}, "hive:sender", true, uint(100_000_000))

	// decision on escrow by sender
	CallContract(t, ct, "e_decide",
		[]byte("0|f"),
		nil, "hive:sender", true, uint(100_000_000))

	// decision on escrow by receiver
	CallContract(t, ct, "e_decide",
		[]byte("0|r"),
		nil, "hive:receiver", true, uint(100_000_000))

	// decision on escrow by arbitrator
	CallContract(t, ct, "e_decide",
		[]byte("0|f"),
		nil, "hive:arbitrator", true, uint(100_000_000))
	// PrintBalances(ct, []string{"hive:sender", "hive:receiver"})
	bal := ct.GetBalance("hive:sender", ledgerDb.AssetHive)
	assert.Equal(t, int64(1000), bal)
	CallContract(t, ct, "e_get", []byte("0"), nil, "hive:sender", true, uint(100_000_000))
}
