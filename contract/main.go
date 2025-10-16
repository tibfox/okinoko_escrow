package main

import (
	"okinoko_escrow/sdk"
	"strconv"
	"strings"
)

func main() {
	// exact code within other files of conrtact folder
}

const (
	maxNameLength = 256 // maxNameLength for any escrow
)

type EscrowAccount struct {
	Address      sdk.Address `json:"a"`
	Agree        *bool       `json:"ag"`
	DecisionTxID *string     `json:"dTx"`
}

type Escrow struct {
	ID           uint64        `json:"id"`
	Name         string        `json:"n"`
	From         EscrowAccount `json:"f"`
	To           EscrowAccount `json:"t"`
	Arbitrator   EscrowAccount `json:"arb"`
	CreationTxID string        `json:"cTx"`
	AmountMilli  uint64        `json:"am"` // stored in thousandths (1.234 = 1234)
	Asset        sdk.Asset     `json:"as"`
	Closed       bool          `json:"cl"` // false = open / true = closed
	Outcome      string        `json:"o"`
}

type CreateEscrowArgs struct {
	Name       string `json:"name"`
	To         string `json:"to"`
	Arbitrator string `json:"arb"`
}

type DecisionArgs struct {
	EscrowID *uint64 `json:"id"`
	Decision *bool   `json:"d"`
}

const (
	OutcomeRelease = "r"
	OutcomeRefund  = "f"
	OutcomePending = "p"
)

//go:wasmexport e_create
func CreateEscrow(payload *string) *string {
	input := fastParseCreateEscrowArgs(*payload)
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
	txID := sdk.GetEnvKey("tx.id")
	escrow := Escrow{
		ID: escrowID,
		From: EscrowAccount{
			Address: sdk.Address(*creator),
			Agree:   nil,
		},
		To: EscrowAccount{
			Address: sdk.Address(input.To),
			Agree:   nil,
		},
		Arbitrator: EscrowAccount{
			Address: sdk.Address(input.Arbitrator),
			Agree:   nil,
		},
		Name:         input.Name,
		CreationTxID: *txID,
		AmountMilli:  ta.LimitMilli,
		Asset:        ta.Token,
		Outcome:      OutcomePending,
	}
	sdk.HiveDraw(int64(ta.LimitMilli), ta.Token)
	saveEscrowBase(&escrow)
	setCount(EscrowCount, escrow.ID+1)
	// Emit creation event.
	EmitEscrowCreatedEvent(
		escrow.ID,
		escrow.From.Address.String(),
		escrow.To.Address.String(),
		escrow.Arbitrator.Address.String(),
		float64(escrow.AmountMilli)/1000,
		escrow.Asset.String(), *txID)

	result := strconv.FormatUint(escrowID, 10)
	return &result
}

//go:wasmexport e_decide
func AddDecision(payload *string) *string {
	input := fastParseDecisionArgs(*payload)
	input.Validate()

	id := *input.EscrowID
	baseKey := escrowKey(id)
	e := loadEscrowBase(id) // loads static escrow data only

	if e.Closed {
		sdk.Abort("escrow closed")
	}

	// Load all participant accounts
	f := loadEscrowAccount(baseKey + ":f")
	t := loadEscrowAccount(baseKey + ":t")
	a := loadEscrowAccount(baseKey + ":arb")
	if f == nil || t == nil || a == nil {
		sdk.Abort("escrow accounts missing")
	}
	e.From, e.To, e.Arbitrator = *f, *t, *a

	sender := sdk.GetEnvKey("msg.sender")
	txID := sdk.GetEnvKey("tx.id")

	// Inline role + suffix selection (no helper calls)
	var key string
	switch *sender {
	case e.From.Address.String():
		key = baseKey + ":f"
		e.From.Agree = input.Decision
		e.From.DecisionTxID = txID
	case e.To.Address.String():
		key = baseKey + ":t"
		e.To.Agree = input.Decision
		e.To.DecisionTxID = txID
	case e.Arbitrator.Address.String():
		key = baseKey + ":arb"
		e.Arbitrator.Agree = input.Decision
		e.Arbitrator.DecisionTxID = txID
	default:
		sdk.Abort("sender not part of the escrow")
	}

	// Persist updated account immediately
	switch key[len(baseKey)+1:] {
	case "f":
		sdk.StateSetObject(key, fastJSONEscrowAccount(&e.From))
		EmitEscrowDecisionEvent(id, "From", *sender, *input.Decision, *txID)
	case "t":
		sdk.StateSetObject(key, fastJSONEscrowAccount(&e.To))
		EmitEscrowDecisionEvent(id, "To", *sender, *input.Decision, *txID)
	case "arb":
		sdk.StateSetObject(key, fastJSONEscrowAccount(&e.Arbitrator))
		EmitEscrowDecisionEvent(id, "Arbitrator", *sender, *input.Decision, *txID)
	}

	// Re-evaluate escrow state (using in-memory data only)
	e.EvaluateEscrowOutcome()

	if e.Closed {
		EmitEscrowClosedEvent(e.ID, e.Outcome, *txID)
		saveEscrowOutcome(e)
	}

	return nil
}

// GETTERS

//go:wasmexport e_get
func GetEscrow(id *string) *string {
	if id == nil {
		sdk.Abort("input is empty")
	}
	eId, err := strconv.ParseUint(*id, 10, 64)
	if err != nil {
		sdk.Abort("error converting escrow id")
	}

	e := loadEscrowBase(eId)
	baseKey := escrowKey(eId)
	if f := loadEscrowAccount(baseKey + ":f"); f != nil {
		e.From = *f
	}
	if t := loadEscrowAccount(baseKey + ":t"); t != nil {
		e.To = *t
	}
	if a := loadEscrowAccount(baseKey + ":arb"); a != nil {
		e.Arbitrator = *a
	}

	j := fastJSONEscrow(e)
	return &j
}

// STATE PERSISTENCE & LOADING

func loadEscrowBase(id uint64) *Escrow {
	key := escrowKey(id)
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort("escrow not found")
	}
	e := fastParseEscrow(*ptr)
	return e
}

func loadEscrowAccount(key string) *EscrowAccount {
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		return nil
	}
	return fastParseEscrowAccount(*ptr)
}

func saveEscrowOutcome(e *Escrow) {
	key := escrowKey(e.ID)
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort("base escrow missing")
	}

	src := *ptr
	outcomeEsc := escapeJSONString(e.Outcome)

	// Preallocate output buffer about same size
	buf := make([]byte, 0, len(src)+8)

	for i := 0; i < len(src); {
		// Fast-scan for our two targets
		if i+8 < len(src) && src[i] == '"' && src[i+1] == 'c' && src[i+2] == 'l' && src[i+3] == '"' {
			// Replace "cl":false → "cl":true
			buf = append(buf, `"cl":true`...)
			i += 10 // skip `"cl":false`
			continue
		}
		if i+12 < len(src) && src[i] == '"' && src[i+1] == 'o' && src[i+2] == '"' &&
			src[i+3] == ':' && src[i+4] == '"' && src[i+5] == 'p' && src[i+6] == 'e' &&
			src[i+7] == 'n' && src[i+8] == 'd' && src[i+9] == 'i' && src[i+10] == 'n' && src[i+11] == 'g' {
			// Replace "o":"pending" → "o":"<escaped>"
			buf = append(buf, `"o":"`...)
			buf = append(buf, outcomeEsc...)
			buf = append(buf, '"')
			// skip `"o":"pending"`
			i += 13
			continue
		}
		buf = append(buf, src[i])
		i++
	}

	sdk.StateSetObject(key, string(buf))
}

func saveEscrowBase(e *Escrow) {
	// Reuse a single buffer; 256 B covers almost all escrow JSON
	buf := make([]byte, 0, 256)

	buf = append(buf, `{"id":`...)
	buf = strconv.AppendUint(buf, e.ID, 10)
	buf = append(buf, `,"n":"`...)
	buf = append(buf, escapeJSONString(e.Name)...)
	buf = append(buf, `","cTx":"`...)
	buf = append(buf, escapeJSONString(e.CreationTxID)...)
	buf = append(buf, `","am":`...)
	buf = append(buf, forceFloatString(float64(e.AmountMilli)/1000)...)
	buf = append(buf, `,"as":"`...)
	buf = append(buf, escapeJSONString(e.Asset.String())...)
	buf = append(buf, `","cl":`...)
	buf = strconv.AppendBool(buf, e.Closed)
	buf = append(buf, `,"o":"`...)
	buf = append(buf, escapeJSONString(e.Outcome)...)
	buf = append(buf, `"}`...)

	// Write the main object
	sdk.StateSetObject(escrowKey(e.ID), string(buf))

	// participant base accounts (addresses only)
	base := escrowKey(e.ID)
	sdk.StateSetObject(base+":f", fastJSONEscrowAccount(&e.From))
	sdk.StateSetObject(base+":t", fastJSONEscrowAccount(&e.To))
	sdk.StateSetObject(base+":arb", fastJSONEscrowAccount(&e.Arbitrator))
}

// VALIDATORS

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
	if c.Arbitrator == c.To || c.Arbitrator == callerAddress {
		sdk.Abort("arbitrator must be 3rd party")
	}
}

func (c *DecisionArgs) Validate() {
	if c.EscrowID == nil {
		sdk.Abort("escrow id is mandatory")
	}
	if c.Decision == nil {
		sdk.Abort("decision is not a correct boolean")
	}
}

// COMMON HELPERS

func escrowKey(escrowID uint64) string {
	return "e:" + strconv.FormatUint(escrowID, 10)
}

func (e *Escrow) EvaluateEscrowOutcome() {
	if e.Closed {
		sdk.Log("escrow already closed")
		return
	}
	releaseCount, refundCount := e.CountDecisions()
	if releaseCount >= 2 {
		e.Closed = true
		e.Outcome = OutcomeRelease
		sdk.HiveTransfer(e.To.Address, int64(e.AmountMilli), e.Asset)
	} else if refundCount >= 2 {
		e.Closed = true
		e.Outcome = OutcomeRefund
		sdk.HiveTransfer(e.From.Address, int64(e.AmountMilli), e.Asset)
	}
}

func (e *Escrow) CountDecisions() (releaseCount, refundCount int) {
	accounts := []EscrowAccount{e.From, e.To, e.Arbitrator}

	for _, acc := range accounts {
		if acc.Agree == nil {
			continue // no decision yet
		}
		if *acc.Agree {
			releaseCount++
		} else {
			refundCount++
		}
	}
	return
}

// HELPERS

const (
	EscrowCount = "cnt:e" //                  // holds a int counter for escrows (to create new ids)
)

func setCount(key string, n uint64) {
	sdk.StateSetObject(key, strconv.FormatUint(n, 10))
}

type TransferAllow struct {
	LimitMilli uint64
	Token      sdk.Asset
}

// validAssets defines the list of supported assets for transfer intents.
var validAssets = []string{sdk.AssetHbd.String(), sdk.AssetHive.String()}

// isValidAsset checks whether a given token string is a supported asset.
func isValidAsset(token string) bool {
	for _, a := range validAssets {
		if token == a {
			return true
		}
	}
	return false
}

// parseLimitMilli floors to 3 decimals and rejects negatives and bad formats.
// Examples: "1" -> 1000, "1.0" -> 1000, "1.0019" -> 1001, "0.0009" -> 0 (will be rejected later)
func parseLimitMilli(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	// no leading +/-, exponent, or spaces allowed
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
			// intPart = intPart*10 + d
			// multiply by 10 with overflow check (practically safe for typical limits)
			intPart = intPart*10 + d
			if intPart > ^uint64(0)/1000 {
				return 0, false
			}
		} else if fracDigits < 3 {
			// take up to first 3 fractional digits; floor extra
			fracPart = fracPart*10 + d
			fracDigits++
		} else {
			// extra fractional digits are ignored (floor)
		}
	}

	// pad fractional to 3 digits
	for fracDigits < 3 {
		fracPart *= 10
		fracDigits++
	}

	// milli = intPart*1000 + fracPart
	return intPart*1000 + fracPart, true
}

// GetFirstTransferAllow searches the provided intents and returns the first
// valid transfer.allow intent as a TransferAllow. Returns nil if none exist.
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

// EVENTS

// Event represents a generic event emitted by the contract.
type Event struct {
	Type       string            `json:"t"`   // Type is the kind of event (e.g., "mint", "transfer").
	Attributes map[string]string `json:"att"` // Attributes are key/value pairs with event data.
	TxID       string            `json:"tx"`
}

// emitEvent constructs and logs an event as JSON.
func emitEvent(eventType string, attributes map[string]string, txID string) {
	sdk.Log(fastJSONEvent(eventType, attributes, txID))
}

// EmitEscrowCreatedEvent emits an event for creating a new escrow.
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
func EmitEscrowDecisionEvent(escrowID uint64, role string, address string, decision bool, txID string) {
	emitEvent("de", map[string]string{
		"id": strconv.FormatUint(escrowID, 10),
		"r":  role,
		"a":  address,
		"d":  strconv.FormatBool(decision),
	}, txID)
}

// EmitEscrowClosedEvent emits an event for closing an escrow.
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
