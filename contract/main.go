package main

import (
	"okinoko_escrow/sdk"
	"strconv"
	"strings"
)

// main is required for WASM targets; contract logic is exposed via exported functions.
func main() {}

const (
	maxNameLength = 100

	// DecisionUnset indicates no decision has been made.
	DecisionUnset uint8 = 0
	// DecisionRefund indicates a refund decision.
	DecisionRefund uint8 = 1
	// DecisionRelease indicates a release decision.
	DecisionRelease uint8 = 2
)

// =====================
// Data Types
// =====================

// EscrowAccount represents an escrow participant and their decision.
type EscrowAccount struct {
	Address  string `json:"a"`
	Decision string `json:"d"`
}

// Escrow describes an escrow instance and its state.
type Escrow struct {
	ID         uint64        `json:"id"`
	Name       string        `json:"n"`
	From       EscrowAccount `json:"f"`
	To         EscrowAccount `json:"t"`
	Arbitrator EscrowAccount `json:"arb"`
	Amount     float64       `json:"am"`
	Asset      string        `json:"as"`
	Closed     bool          `json:"cl"`
	Outcome    uint8         `json:"o"`
}

// CreateEscrowArgs are arguments to create a new escrow.
type CreateEscrowArgs struct {
	Name       string
	To         string
	Arbitrator string
}

// DecisionArgs are arguments to add a decision to an escrow.
type DecisionArgs struct {
	EscrowID uint64
	Decision uint8
}

// =====================
// Parsing Utilities
// =====================

// CsvToCreateEscrowArgs parses a pipe-delimited string into CreateEscrowArgs (Name|To|Arbitrator).
func CsvToCreateEscrowArgs(csv *string) CreateEscrowArgs {
	if csv == nil || *csv == "" {
		sdk.Abort("input CSV is nil or empty")
	}

	parts := strings.Split(*csv, "|")
	if len(parts) != 3 {
		sdk.Abort("invalid CSV format: expected 3 fields (Name|To|Arbitrator)")
	}

	return CreateEscrowArgs{
		Name:       parts[0],
		To:         parts[1],
		Arbitrator: parts[2],
	}
}

// CsvToDecisionArgs parses a pipe-delimited string into DecisionArgs (EscrowID|Decision).
// Decision accepts true/false (case-insensitive) or 1/0.
func CsvToDecisionArgs(csv *string) DecisionArgs {
	if csv == nil || *csv == "" {
		sdk.Abort("input CSV is nil or empty")
	}

	data := *csv
	sep := strings.IndexByte(data, '|')
	if sep == -1 {
		sdk.Abort("invalid CSV format: expected EscrowID|Decision")
	}

	// Parse escrow ID (left of '|').
	escrowIDValue, err := strconv.ParseUint(data[:sep], 10, 64)
	if err != nil {
		sdk.Abort("invalid EscrowID: must be a number")
	}

	// Parse decision (right of '|'), trimming spaces without allocations.
	decStr := data[sep+1:]
	i, j := 0, len(decStr)-1
	for i <= j && decStr[i] == ' ' {
		i++
	}
	for j >= i && decStr[j] == ' ' {
		j--
	}
	decStr = decStr[i : j+1]

	var decision uint8
	// Map common boolean/bit encodings to protocol decisions.
	switch decStr {
	case "r":
		decision = DecisionRelease
	case "f":
		decision = DecisionRefund
	default:
		sdk.Abort("invalid decision: must be r/f")
	}

	return DecisionArgs{
		EscrowID: escrowIDValue,
		Decision: decision,
	}
}

// CsvToReward parses a pipe-delimited reward string into amount and asset (amount|asset).
func CsvToReward(csv *string) (uint64, string) {
	if csv == nil || *csv == "" {
		sdk.Abort("reward is empty")
	}

	data := *csv
	sep := strings.IndexByte(data, '|')
	if sep == -1 {
		sdk.Abort("invalid reward format, expected amount|asset")
	}

	amount, err := strconv.ParseUint(data[:sep], 10, 64)
	if err != nil {
		sdk.Abort("error parsing amount")
	}
	return amount, data[sep+1:]
}

// =====================
// WASM Exports
// =====================

// CreateEscrow creates a new escrow from the provided payload.
//
//go:wasmexport e_create
func CreateEscrow(payload *string) *string {
	input := CsvToCreateEscrowArgs(payload)
	creator := sdk.GetEnvKey("msg.sender")

	input.Validate(*creator)

	var escrowID uint64
	ptr := sdk.StateGetObject(EscrowCount)
	if ptr == nil || *ptr == "" {
		escrowID = 0
	} else if id, err := strconv.ParseUint(*ptr, 10, 64); err == nil {
		escrowID = id
	}
	ta := GetFirstTransferAllow(sdk.GetEnv().Intents)
	if ta == nil {
		sdk.Abort("intent needed")
	}
	if ta.LimitMilli <= 0 {
		sdk.Abort("intent >0 needed")
	}
	if !isValidAsset(ta.Token.String()) {
		sdk.Abort("intent asset not supported")
	}
	if input.To == *creator {
		sdk.Abort("receiver must differ from sender")
	}

	// Lock funds into escrow as per the transfer.allow intent.
	sdk.HiveDraw(int64(ta.LimitMilli), ta.Token)

	// Persist base escrow name.
	saveEscrowBase(escrowID, input.Name)

	// Persist roles as a compact pipe-delimited string: from|to|arb.
	var sb strings.Builder
	sb.Grow(len(*creator) + len(input.To) + len(input.Arbitrator) + 2)
	sb.WriteString(*creator)
	sb.WriteByte('|')
	sb.WriteString(input.To)
	sb.WriteByte('|')
	sb.WriteString(input.Arbitrator)
	saveEscrowParties(escrowID, sb.String())

	// Persist reward (amount + asset).
	saveEscrowReward(escrowID, ta.LimitMilli, ta.Token.String())

	// Initialize decisions (unset for all three parties).
	saveEscrowDecisions(escrowID, []uint8{DecisionUnset, DecisionUnset, DecisionUnset})

	// Emit creation event and return escrow ID.
	txID := sdk.GetEnvKey("tx.id")
	EmitEscrowCreatedEvent(
		escrowID,
		*creator,
		input.To,
		input.Arbitrator,
		float64(ta.LimitMilli)/1000,
		ta.Token.String(), *txID)

	result := strconv.FormatUint(escrowID, 10)
	return &result
}

// AddDecision records a decision for the sender in the given escrow.
//
//go:wasmexport e_decide
func AddDecision(payload *string) *string {
	input := CsvToDecisionArgs(payload)
	roles := loadRoles(input.EscrowID)
	sender := sdk.GetEnvKey("msg.sender")
	txID := sdk.GetEnvKey("tx.id")

	role := getRoleOfSender(sender, roles)
	if role == nil {
		sdk.Abort("sender not part of the escrow")
	}

	decs := loadDecisions(input.EscrowID)

	// Disallow voting on a closed escrow.
	if closed, _ := getEscrowOutcome(decs); closed {
		sdk.Abort("escrow already closed")
	}

	// Record this sender's decision in their role slot.
	roleIndex := *role
	decs[roleIndex] = input.Decision

	// Persist decision updates and process possible outcome.
	saveEscrowDecisions(input.EscrowID, decs)
	txID := sdk.GetEnvKey("tx.id")
	processEscrowOutcome(input.EscrowID, decs, *txID)
	EmitEscrowDecisionEvent(
		input.EscrowID,
		friendlyRoleName(*role),
		*sender,
		input.Decision,
		*txID)
	return nil
}

// GetEscrow returns escrow details by ID.
//
//go:wasmexport e_get
func GetEscrow(id *string) *string {
	escrowBase := sdk.StateGetObject(*id)
	if escrowBase == nil || *escrowBase == "" {
		sdk.Abort(fmt.Sprintf("escrow %s not found", *id))
	}
	uintId := StringToUInt64(id)
	escrowParties := loadRoles(uintId)
	am, as := loadReward(uintId)
	escrowDecisions := loadDecisions(uintId)
	c, o := getEscrowOutcome(escrowDecisions)
	escrow := &Escrow{
		ID:   uintId,
		Name: *escrowBase,
		From: EscrowAccount{
			Address:  escrowParties[0],
			Decision: friendlyOutcome(escrowDecisions[0]),
		},
		To: EscrowAccount{
			Address:  escrowParties[1],
			Decision: friendlyOutcome(escrowDecisions[1]),
		},
		Arbitrator: EscrowAccount{
			Address:  escrowParties[2],
			Decision: friendlyOutcome(escrowDecisions[2]),
		},
		Amount:  float64(am) / 1000,
		Asset:   as,
		Closed:  c,
		Outcome: o,
	}

	jsonStr := ToJSON(escrow, "escrow")
	return &jsonStr
}

// =====================
// State Persistence & Loading
// =====================

// saveEscrowBase stores the base escrow name and increments the global counter.
func saveEscrowBase(escrowID uint64, escrowCsv string) error {
	key := strconv.FormatUint(escrowID, 10)
	sdk.StateSetObject(key, escrowCsv)
	setEscrowCount(escrowID + 1)
	return nil
}

// saveEscrowParties stores the from|to|arb addresses for an escrow.
func saveEscrowParties(escrowID uint64, escrowPartiesCsv string) error {
	key := strconv.FormatUint(escrowID, 10) + "|p"
	sdk.StateSetObject(key, escrowPartiesCsv)
	return nil
}

// saveEscrowReward stores the amount (milli) and asset for an escrow.
func saveEscrowReward(escrowID uint64, amount uint64, asset string) error {
	key := strconv.FormatUint(escrowID, 10) + "|r"
	buf := make([]byte, 0, 32+len(asset))
	buf = strconv.AppendUint(buf, amount, 10)
	buf = append(buf, '|')
	buf = append(buf, asset...)
	sdk.StateSetObject(key, string(buf))
	return nil
}

// saveEscrowDecisions stores the three decision bytes (from, to, arb).
func saveEscrowDecisions(escrowID uint64, decs []uint8) error {
	b := make([]byte, len(decs))
	copy(b, decs)
	key := strconv.FormatUint(escrowID, 10) + "|d"
	sdk.StateSetObject(key, string(b))
	return nil
}

// loadRoles retrieves the from|to|arb addresses for an escrow.
func loadRoles(escrowID uint64) []string {
	key := strconv.FormatUint(escrowID, 10) + "|p"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("parties for escrow %d not found", escrowID))
	}
	data := *ptr
	start := 0
	roles := make([]string, 0, 3)
	for i := 0; i < len(data); i++ {
		if data[i] == '|' {
			roles = append(roles, data[start:i])
			start = i + 1
		}
	}
	roles = append(roles, data[start:])
	if len(roles) != 3 {
		sdk.Abort("invalid parties length")
	}
	return roles
}

// loadDecisions retrieves the three decision bytes (from, to, arb).
func loadDecisions(escrowID uint64) []uint8 {
	key := strconv.FormatUint(escrowID, 10) + "|d"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("decisions for escrow %d not found", escrowID))
	}
	data := []byte(*ptr)
	if len(data) != 3 {
		sdk.Abort("invalid decisions length")
	}
	decs := make([]uint8, 3)
	copy(decs, data)
	return decs
}

// =====================
// Validators
// =====================

// Validate checks the semantic correctness of CreateEscrowArgs.
func (c *CreateEscrowArgs) Validate(callerAddress string) {
	if c.Name == "" {
		sdk.Abort("name is mandatory")
	}
	if len(c.Name) > maxNameLength {
		sdk.Abort("name too long")
	}
	if c.To == "" {
		sdk.Abort("receiver is mandatory")
	}
	if c.Arbitrator == "" {
		sdk.Abort("arbitrator is mandatory")
	}
	// Arbitrator must be neutral and not overlap with participants.
	if c.Arbitrator == c.To || c.Arbitrator == callerAddress {
		sdk.Abort("arbitrator must be 3rd party")
	}
}

// =====================
// Common Helpers
// =====================

// getRoleOfSender returns the role index (0=from,1=to,2=arb) of the sender, if any.
func getRoleOfSender(sender *string, parties []string) *uint8 {
	if sender == nil {
		return nil
	}
	for i, p := range parties {
		if p == *sender {
			role := uint8(i)
			return &role
		}
	}
	return nil
}

// getEscrowOutcome determines whether the escrow is closed and its outcome.
// The escrow closes when at least two parties agree on the same decision.
func getEscrowOutcome(decs []uint8) (bool, uint8) {
	counts := [3]uint8{}
	for _, d := range decs {
		if d > DecisionRelease {
			sdk.Abort("invalid decision value in state")
		}
		if d != DecisionUnset {
			counts[d]++
			if counts[d] >= 2 {
				return true, d
			}
		}
	}
	return false, DecisionUnset
}

// loadReward retrieves the escrow amount (milli) and asset.
func loadReward(escrowID uint64) (amount uint64, asset string) {
	key := strconv.FormatUint(escrowID, 10) + "|r"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("amount for escrow %d not found", escrowID))
	}
	return CsvToReward(ptr)
}

// friendlyRoleName returns the compact role label used in events.
func friendlyRoleName(r uint8) string {
	switch r {
	case 0:
		return "f"
	case 1:
		return "t"
	default:
		return "arb"
	}
}

// processEscrowOutcome finalizes transfers and emits a close event when consensus is reached.
func processEscrowOutcome(escrowID uint64, decs []uint8, txId string) {
	if closed, outcome := getEscrowOutcome(decs); closed {
		am, as := loadReward(escrowID)
		r := loadRoles(escrowID)

		// Route funds based on outcome consensus.
		switch outcome {
		case DecisionRefund:
			sdk.HiveTransfer(sdk.Address(r[0]), int64(am), sdk.Asset(as)) // creator
		case DecisionRelease:
			sdk.HiveTransfer(sdk.Address(r[1]), int64(am), sdk.Asset(as)) // receiver
		}

		EmitEscrowClosedEvent(escrowID, friendlyOutcome(outcome), txId)
	}
}

// friendlyOutcome returns a human-readable outcome label.
func friendlyOutcome(o uint8) string {
	switch o {
	case DecisionRefund:
		return "f"
	case DecisionRelease:
		return "r"
	default:
		return "p"
	}
}

// ToJSON marshals a value as JSON, aborting on error.
func ToJSON[T any](v T, objectType string) string {
	b, err := json.Marshal(v)
	if err != nil {
		sdk.Abort("failed to marshal " + objectType)
	}
	return string(b)
}

// StringToUInt64 parses a decimal string into a uint64, aborting on error.
func StringToUInt64(ptr *string) uint64 {
	if ptr == nil {
		sdk.Abort("input is empty")
	}
	val, err := strconv.ParseUint(*ptr, 10, 64)
	if err != nil {
		sdk.Abort(fmt.Sprintf("failed to parse '%s' to uint64: %v", *ptr, err))
	}
	return val
}

// newEscrowID reads the next escrow ID from state; defaults to 0 if unset.
func newEscrowID() uint64 {
	ptr := sdk.StateGetObject("cnt:e")
	if ptr == nil || *ptr == "" {
		return 0
	}
	return StringToUInt64(ptr)
}

// setEscrowCount persists the next escrow ID counter.
func setEscrowCount(n uint64) {
	sdk.StateSetObject("cnt:e", strconv.FormatUint(n, 10))
}

// =====================
// Transfer-Allow Intent
// =====================

// TransferAllow represents a parsed transfer.allow intent.
type TransferAllow struct {
	LimitMilli uint64
	Token      sdk.Asset
}

var validAssets = []string{sdk.AssetHbd.String(), sdk.AssetHive.String()}

// isValidAsset checks the token against supported assets.
func isValidAsset(token string) bool {
	for _, a := range validAssets {
		if token == a {
			return true
		}
	}
	return false
}

// GetFirstTransferAllow returns the first valid transfer.allow intent or nil.
func GetFirstTransferAllow(intents []sdk.Intent) *TransferAllow {
	for _, intent := range intents {
		if intent.Type == "transfer.allow" {
			token := intent.Args["token"]
			if !isValidAsset(token) {
				sdk.Abort("invalid intent token")
			}
			limitStr := intent.Args["limit"]
			milli, ok := parseLimitMilli(limitStr)
			if !ok {
				sdk.Abort("invalid intent limit")
			}
			if milli == 0 {
				sdk.Abort("intent >0 needed")
			}
			return &TransferAllow{
				LimitMilli: milli,
				Token:      sdk.Asset(token),
			}
		}
	}
	return nil
}

// parseLimitMilli parses a decimal string into thousandths (milli) with up to 3 fractional digits.
// Extra fractional precision is truncated (floor).
func parseLimitMilli(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	// Reject spaces/signs/exponents; allow digits and a single dot.
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && c != '.' {
			return 0, false
		}
	}

	var intPart uint64
	var fracPart uint64
	var fracDigits int
	sawDot := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if sawDot {
				return 0, false
			}
			sawDot = true
			continue
		}
		d := uint64(c - '0')
		if !sawDot {
			intPart = intPart*10 + d
			// Cheap overflow guard for later *1000.
			if intPart > ^uint64(0)/1000 {
				return 0, false
			}
		} else if fracDigits < 3 {
			fracPart = fracPart*10 + d
			fracDigits++
		}
	}

	// Pad to milli.
	for fracDigits < 3 {
		fracPart *= 10
		fracDigits++
	}

	return intPart*1000 + fracPart, true
}

// =====================
// Events
// =====================

// Event represents a generic contract event.
type Event struct {
	Type       string            `json:"type"`
	Attributes map[string]string `json:"attributes"`
	TxID       string            `json:"tx"`
}

// emitEvent logs an event as JSON.
func emitEvent(eventType string, attributes map[string]string, txID string) {
	sdk.Log(fastJSONEvent(eventType, attributes, txID))
}

// EmitEscrowCreatedEvent emits an event for a newly created escrow.
func EmitEscrowCreatedEvent(escrowID uint64, fromAddress string, toAddress string, arbAddress string, amount float64, asset string, txID string) {
	emitEvent("cr", map[string]string{
		"id":  strconv.FormatUint(escrowID, 10),
		"f":   fromAddress,
		"t":   toAddress,
		"arb": arbAddress,
		"am":  strconv.FormatFloat(amount, 'f', -1, 64),
		"as":  asset,
	}, txID)
}

// EmitEscrowDecisionEvent emits an event for a new decision.
func EmitEscrowDecisionEvent(escrowID uint64, role string, address string, decisionId uint8, txID string) {
	emitEvent("de", map[string]string{
		"id": strconv.FormatUint(escrowID, 10),
		"r":  role,
		"a":  address,
		"d":  friendlyOutcome(decisionId),
	}, txID)
}

// EmitEscrowClosedEvent emits an event for a closed escrow.
func EmitEscrowClosedEvent(escrowID uint64, outcome string, txID string) {
	emitEvent("cl", map[string]string{
		"id": strconv.FormatUint(escrowID, 10),
		"o":  outcome,
	}, txID)
}

// JSON HELPERS (to reduce gas by~50% )

// ---------- EscrowAccount ----------

func fastJSONEscrowAccount(a *EscrowAccount) string {
	if a == nil {
		return `{"a":"","ag":null,"dTx":null}`
	}
	addr := a.Address.String()

	// pre-size to avoid realloc
	out := make([]byte, 0, 96)
	out = append(out, `{"a":"`...)
	out = append(out, escapeJSONString(addr)...)
	out = append(out, `","ag":`...)

	if a.Agree == nil {
		out = append(out, "null"...)
	} else if *a.Agree {
		out = append(out, "true"...)
	} else {
		out = append(out, "false"...)
	}

	out = append(out, `,"dTx":`...)
	if a.DecisionTxID == nil {
		out = append(out, "null"...)
	} else {
		out = append(out, '"')
		out = append(out, escapeJSONString(*a.DecisionTxID)...)
		out = append(out, '"')
	}
	out = append(out, '}')
	return string(out)
}

func fastParseEscrowAccount(data string) *EscrowAccount {
	// This expects same shape produced by fastJSONEscrowAccount.
	a := &EscrowAccount{}
	getStr := func(key string) string {
		idx := strings.Index(data, `"`+key+`":"`)
		if idx == -1 {
			return ""
		}
		start := idx + len(key) + 4
		end := strings.IndexByte(data[start:], '"')
		if end == -1 {
			return ""
		}
		return data[start : start+end]
	}

	a.Address = sdk.Address(getStr("a"))
	if strings.Contains(data, `"ag":true`) {
		t := true
		a.Agree = &t
	} else if strings.Contains(data, `"ag":false`) {
		f := false
		a.Agree = &f
	}
	if tx := getStr("dTx"); tx != "" {
		a.DecisionTxID = &tx
	}
	return a
}

// ---------- Escrow ----------

func fastJSONEscrow(e *Escrow) string {
	out := make([]byte, 0, 512)

	out = append(out, `{"id":`...)
	out = strconv.AppendUint(out, e.ID, 10)
	out = append(out, `,"n":"`...)
	out = append(out, escapeJSONString(e.Name)...)
	out = append(out, `","f":`...)
	out = append(out, fastJSONEscrowAccount(&e.From)...)
	out = append(out, `,"t":`...)
	out = append(out, fastJSONEscrowAccount(&e.To)...)
	out = append(out, `,"arb":`...)
	out = append(out, fastJSONEscrowAccount(&e.Arbitrator)...)
	out = append(out, `,"cTx":"`...)
	out = append(out, escapeJSONString(e.CreationTxID)...)
	out = append(out, `","am":`...)
	out = append(out, forceFloatString(float64(e.AmountMilli)/1000)...)
	out = append(out, `,"as":"`...)
	out = append(out, escapeJSONString(e.Asset.String())...)
	out = append(out, `","cl":`...)
	out = strconv.AppendBool(out, e.Closed)
	out = append(out, `,"o":"`...)
	out = append(out, escapeJSONString(e.Outcome)...)
	out = append(out, `"}`...)
	return string(out)
}

// forceFloatString ensures that even whole numbers render with a decimal point (e.g., 1 -> "1.0").
func forceFloatString(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == 'e' || c == 'E' {
			return s
		}
	}
	return s + ".000"
}

func fastParseEscrow(data string) *Escrow {
	e := &Escrow{}

	// strings (quoted)
	getStr := func(key string) string {
		idx := strings.Index(data, `"`+key+`":"`)
		if idx == -1 {
			return ""
		}
		start := idx + len(key) + 4
		end := strings.IndexByte(data[start:], '"')
		if end == -1 {
			return ""
		}
		return data[start : start+end]
	}

	// id (number, unquoted)
	if idNum := grabNumber(data, "id"); idNum != "" {
		if id, err := strconv.ParseUint(idNum, 10, 64); err == nil {
			e.ID = id
		}
	}

	e.Name = getStr("n")
	e.CreationTxID = getStr("cTx")
	e.Outcome = getStr("o")

	// amount (number, unquoted)
	if amNum := grabNumber(data, "am"); amNum != "" {
		if am, err := strconv.ParseFloat(amNum, 64); err == nil {
			e.AmountMilli = uint64(am * 1000)
		}
	}

	e.Asset = sdk.Asset(getStr("as"))
	e.Closed = strings.Contains(data, `"cl":true`)

	// subaccounts
	if idx := strings.Index(data, `"f":{`); idx != -1 {
		if end := strings.Index(data[idx:], `},"t":`); end > 0 {
			e.From = *fastParseEscrowAccount(data[idx+4 : idx+end+1])
		}
	}
	if idx := strings.Index(data, `"t":{`); idx != -1 {
		if end := strings.Index(data[idx:], `},"arb":`); end > 0 {
			e.To = *fastParseEscrowAccount(data[idx+4 : idx+end+1])
		}
	}
	if idx := strings.Index(data, `"arb":{`); idx != -1 {
		if end := strings.Index(data[idx:], `},"cTx":`); end > 0 {
			e.Arbitrator = *fastParseEscrowAccount(data[idx+7 : idx+end+1])
		}
	}
	return e
}

// ---------- Event ----------

func fastJSONEvent(t string, att map[string]string, txID string) string {
	out := make([]byte, 0, 256)
	out = append(out, `{"t":"`...)
	out = append(out, t...)
	out = append(out, `","att":{`...)

	i := 0
	for k, v := range att {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '"')
		out = append(out, k...)
		out = append(out, `":"`...)
		out = append(out, v...)
		out = append(out, '"')
		i++
	}

	out = append(out, `},"tx":"`...)
	out = append(out, txID...)
	out = append(out, `"}`...)
	return string(out)
}

func fastParseCreateEscrowArgs(data string) *CreateEscrowArgs {
	var args CreateEscrowArgs

	getStr := func(key string) string {
		idx := strings.Index(data, `"`+key+`":"`)
		if idx == -1 {
			return ""
		}
		start := idx + len(key) + 4
		end := strings.IndexByte(data[start:], '"')
		if end == -1 {
			return ""
		}
		return data[start : start+end]
	}

	args.Name = getStr("name")
	args.To = getStr("to")
	args.Arbitrator = getStr("arb")
	return &args
}

func fastParseDecisionArgs(data string) *DecisionArgs {
	var args DecisionArgs
	if i := strings.Index(data, `"id":`); i != -1 {
		start := i + 5
		j := start
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			j++
		}
		if j > start {
			if id, err := strconv.ParseUint(data[start:j], 10, 64); err == nil {
				args.EscrowID = &id
			}
		}
	}
	if strings.Index(data, `"d":true`) != -1 {
		t := true
		args.Decision = &t
	} else if strings.Index(data, `"d":false`) != -1 {
		f := false
		args.Decision = &f
	}
	return &args
}

// escapeJSONString returns a JSON-safe version of s without adding quotes.
// It escapes backslashes, quotes, and control characters like newlines, tabs, etc.
func escapeJSONString(s string) string {
	if len(s) == 0 {
		return ""
	}

	// Pre-allocate 10–20 % extra space for escapes.
	out := make([]byte, 0, len(s)+len(s)/8)

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if c < 0x20 {
				// Control chars → \u00XX form
				out = append(out, '\\', 'u', '0', '0')
				hi := c >> 4
				lo := c & 0xF
				if hi < 10 {
					out = append(out, '0'+hi)
				} else {
					out = append(out, 'a'+hi-10)
				}
				if lo < 10 {
					out = append(out, '0'+lo)
				} else {
					out = append(out, 'a'+lo-10)
				}
			} else {
				out = append(out, c)
			}
		}
	}
	return string(out)
}

// grabNumber extracts an unquoted JSON number following `"key":`
// Supports optional sign, decimal, and exponent; ignores surrounding whitespace.
func grabNumber(data, key string) string {
	idx := strings.Index(data, `"`+key+`":`)
	if idx == -1 {
		return ""
	}
	// position after `"key":`
	i := idx + len(key) + 3
	// skip whitespace
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		}
		break
	}
	// capture number chars
	j := i
	for j < len(data) {
		c := data[j]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
			j++
		} else {
			break
		}
	}
	if j == i {
		return ""
	}
	return data[i:j]
}
