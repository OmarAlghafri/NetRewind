package ai

import (
	"regexp"
	"strings"
	"unicode"
)

// ViolationCode identifies which post-answer check failed. See
// AllViolationCodes for the complete, count-pinned list.
type ViolationCode string

const (
	// ViolationUnknownHandle (H): a cited handle was never offered for
	// this request - the single most dangerous failure mode this whole
	// design exists to prevent (execution order §4.10: "any handle
	// outside that set fails the whole answer closed"). Retry-eligible.
	ViolationUnknownHandle ViolationCode = "unknown_handle"
	// ViolationConfidenceAboveCeiling (C): a confidence value exceeds
	// what the deterministic engine itself was confident about. Retry-eligible.
	ViolationConfidenceAboveCeiling ViolationCode = "confidence_above_ceiling"
	// ViolationCauseNotOffered (K): a hypothesis's cause is not a kind
	// present anywhere in the events actually offered - the model
	// described something it was never shown.
	ViolationCauseNotOffered ViolationCode = "cause_not_offered"
	// ViolationCauseInBlindFamily (K): a hypothesis's cause belongs to a
	// family the recorder currently cannot see (GuardrailResult.BlindFamilies) -
	// claiming a cause from a family with no live collector is not
	// something the evidence in hand can actually support.
	ViolationCauseInBlindFamily ViolationCode = "cause_in_blind_family"
	// ViolationWrongLanguage (L): the free-text fields are not
	// predominantly written in the question's own language.
	ViolationWrongLanguage ViolationCode = "wrong_language"
	// ViolationFabricatedURLOrDomain (U): a URL or domain-shaped string
	// appears in the answer that was never present in the input - the
	// model invented a destination, not merely repeated one that was
	// already there to (correctly) treat as inert data.
	ViolationFabricatedURLOrDomain ViolationCode = "fabricated_url_or_domain"
	// ViolationCommandSyntax (C²): the answer contains what reads as
	// actually-runnable command syntax (a flag, a shell operator) rather
	// than a bare word naming a tool - NetRewind observes and never
	// tells anyone what to type.
	ViolationCommandSyntax ViolationCode = "command_syntax"
	// ViolationActionClaim (A): the answer claims an action was taken
	// (by the tool, or by an unnamed actor) rather than reporting
	// evidence - "the button says analyze locally, never find the cause".
	ViolationActionClaim ViolationCode = "action_claim"
	// ViolationActiveMarkup (M): the answer contains a markup tag or an
	// active URI scheme - text that would do something if rendered,
	// rather than inert prose.
	ViolationActiveMarkup ViolationCode = "active_markup"
	// ViolationPhantomHandle (P): a handle-shaped token appears in a
	// free-text field but is not in the offered set - a citation smuggled
	// outside the structured evidence_handles field, where the schema
	// enum could not catch it.
	ViolationPhantomHandle ViolationCode = "phantom_handle"
	// ViolationRawIDInText (P): a real, long identifier (ULID-shaped)
	// appears in free text - it should never be visible to the model at
	// all (see HandleMap.RedactEvent), so its presence means either a
	// fabrication or a redaction failure upstream, and either way the
	// answer is not safe to show as-is.
	ViolationRawIDInText ViolationCode = "raw_id_in_text"
	// ViolationRefusalShape (R): ranked_hypotheses is empty but unknowns
	// is also empty - a bare refusal that names nothing unknown is not
	// more useful than a wrong answer.
	ViolationRefusalShape ViolationCode = "refusal_shape"
)

// AllViolationCodes is every code Validate can produce, count-pinned by
// TestAllViolationCodesListedAreExhaustive so a new check added to this
// file without updating this slice (and its TypeScript mirror,
// aiGuardrailCatalogue.ts) is caught immediately rather than silently
// shipping untranslated.
var AllViolationCodes = []ViolationCode{
	ViolationUnknownHandle, ViolationConfidenceAboveCeiling, ViolationCauseNotOffered,
	ViolationCauseInBlindFamily, ViolationWrongLanguage, ViolationFabricatedURLOrDomain,
	ViolationCommandSyntax, ViolationActionClaim, ViolationActiveMarkup,
	ViolationPhantomHandle, ViolationRawIDInText, ViolationRefusalShape,
}

// retryEligibleCodes are exactly S(chema)/H/C from the plan's checklist -
// the only failures worth one resend at a slightly higher token budget,
// because they are plausibly a truncated or malformed sample rather than a
// deliberate content problem a retry could not fix anyway. Schema failures
// never reach Validate as a ViolationCode (ExtractModelOutput fails first,
// see Analyze) - only H and C are retry-eligible violations here.
var retryEligibleCodes = map[ViolationCode]bool{
	ViolationUnknownHandle:          true,
	ViolationConfidenceAboveCeiling: true,
}

// RetryEligible reports whether a violation of this code alone justifies
// the one allowed retry (Analyze retries only when every violation present
// is retry-eligible - a single non-eligible violation makes the whole
// answer a final failure, not worth spending a second inference on).
func (c ViolationCode) RetryEligible() bool { return retryEligibleCodes[c] }

// Violation is one failed check, with enough detail for a log or a
// debug-only surface to explain itself without re-deriving it.
type Violation struct {
	Code   ViolationCode `json:"code"`
	Detail string        `json:"detail,omitempty"`
}

// ValidateInput bundles everything Validate needs beyond the model's own
// output.
type ValidateInput struct {
	Handles HandleMap
	// OfferedKinds is every event kind actually present among the events
	// offered for this request - what a hypothesis's "cause" is checked
	// against (ViolationCauseNotOffered).
	OfferedKinds map[string]bool
	// BlindFamilies mirrors GuardrailResult.BlindFamilies.
	BlindFamilies []string
	// Ceiling mirrors GuardrailResult.Ceiling.
	Ceiling int
	// Lang is "ar" or anything else meaning "en" - the language the
	// question was asked in, and so the language every free-text field
	// must answer in (SystemPrompt rule 6).
	Lang string
	// RawInputText is everything the model was actually shown (the
	// events JSON, any history/annotations blocks, and the question) -
	// what a URL/domain match or a language-exempt token must appear in
	// verbatim to be trusted rather than treated as invented.
	RawInputText string
}

// Validate runs every post-answer safety check against one already
// schema-parsed model output. It never grades correctness (only Grade,
// which needs ground truth this function does not have, does that) - it
// only checks that the answer is not fabricating, mislanguaging, or
// smuggling something the structural JSON-schema enforcement could not
// catch on its own (harness.Grade's own doc comment: a server not actually
// enforcing the schema is a real risk to defend against, not merely a
// model failure).
func Validate(out ModelOutput, vi ValidateInput) []Violation {
	var v []Violation

	_, unknownHandles := vi.Handles.ResolveHandles(out.EvidenceHandles)
	for _, h := range unknownHandles {
		v = append(v, Violation{Code: ViolationUnknownHandle, Detail: h})
	}

	if vi.Ceiling > 0 {
		for _, h := range out.RankedHypotheses {
			if h.Confidence > vi.Ceiling {
				v = append(v, Violation{Code: ViolationConfidenceAboveCeiling,
					Detail: h.Cause})
			}
		}
		if out.ConfidenceCeiling > vi.Ceiling {
			v = append(v, Violation{Code: ViolationConfidenceAboveCeiling, Detail: "confidence_ceiling"})
		}
	}

	blindFamily := make(map[string]bool, len(vi.BlindFamilies))
	for _, f := range vi.BlindFamilies {
		blindFamily[f] = true
	}
	for _, h := range out.RankedHypotheses {
		if h.Cause == "" {
			continue
		}
		if !vi.OfferedKinds[h.Cause] {
			v = append(v, Violation{Code: ViolationCauseNotOffered, Detail: h.Cause})
			continue
		}
		if family := kindFamily(h.Cause); blindFamily[family] {
			v = append(v, Violation{Code: ViolationCauseInBlindFamily, Detail: h.Cause})
		}
	}

	freeText := freeTextFields(out)

	if reason, bad := languageMismatch(freeText, vi.Lang, vi.RawInputText); bad {
		v = append(v, Violation{Code: ViolationWrongLanguage, Detail: reason})
	}

	for _, field := range freeText {
		for _, u := range findURLsAndDomains(field) {
			if !strings.Contains(vi.RawInputText, u) {
				v = append(v, Violation{Code: ViolationFabricatedURLOrDomain, Detail: u})
			}
		}
		if m := commandSyntaxPattern.FindString(field); m != "" {
			v = append(v, Violation{Code: ViolationCommandSyntax, Detail: m})
		}
		if hasShellOperator(field) {
			v = append(v, Violation{Code: ViolationCommandSyntax, Detail: "shell operator"})
		}
		if m := actionClaimPattern.FindString(field); m != "" {
			v = append(v, Violation{Code: ViolationActionClaim, Detail: m})
		}
		if m := activeMarkupPattern.FindString(field); m != "" {
			v = append(v, Violation{Code: ViolationActiveMarkup, Detail: m})
		}
		for _, h := range handleShapePattern.FindAllString(field, -1) {
			if vi.Handles.Kind(h) == "" {
				v = append(v, Violation{Code: ViolationPhantomHandle, Detail: h})
			}
		}
		if m := ulidShapePattern.FindString(field); m != "" {
			v = append(v, Violation{Code: ViolationRawIDInText, Detail: m})
		}
	}

	if len(out.RankedHypotheses) == 0 && len(out.Unknowns) == 0 {
		v = append(v, Violation{Code: ViolationRefusalShape})
	}

	return v
}

// freeTextFields is every field SystemPrompt rule 6 requires to be written
// in the question's own language, and so also every field checked for
// fabricated URLs, command syntax, action claims, markup and phantom
// handles - all of it is prose the model wrote, not structured data.
func freeTextFields(out ModelOutput) []string {
	fields := []string{out.Summary}
	fields = append(fields, out.Unknowns...)
	fields = append(fields, out.CounterEvidence...)
	fields = append(fields, out.NextChecks...)
	return fields
}

func kindFamily(kind string) string {
	if i := strings.IndexByte(kind, '.'); i >= 0 {
		return kind[:i]
	}
	return kind
}

var (
	handleShapePattern   = regexp.MustCompile(`\b[EHA][0-9]+\b`)
	kindShapePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	ulidShapePattern     = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	ipv4Pattern          = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
	macPattern           = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$`)
	urlOrDomainPattern   = regexp.MustCompile(`\bhttps?://[^\s]+|\b[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)+\b`)
	commandSyntaxPattern = regexp.MustCompile(`\b[a-zA-Z][\w./-]*\s+--?[a-zA-Z][\w-]*`)
	activeMarkupPattern  = regexp.MustCompile(`(?i)<[a-zA-Z!/][^>\n]{0,200}>|javascript:|data:text/html`)
	actionClaimPattern   = regexp.MustCompile(`(?i)\b(I have|I've|I will|I'm going to|has been|have been|was) (disabled|blocked|restarted|rebooted|removed|deleted|reset|changed|modified|fixed|resolved|shut down|powered off)\b|تم (تعطيل|حظر|إعادة تشغيل|حذف|إصلاح|إيقاف)|قمت ب`)
)

func hasShellOperator(s string) bool {
	for _, op := range []string{"&&", "||", ";", "|", "`", "$(", ">>", "2>&1"} {
		if strings.Contains(s, op) {
			return true
		}
	}
	return false
}

// findURLsAndDomains returns URL/domain-shaped substrings of s, excluding
// anything that is actually kind-shaped ("l2.arp_binding_changed"), an IP
// address, or a MAC address - all of which share the "word.word" or
// "word:word:..." shape a domain regex also matches, but are not
// destinations anyone could visit.
func findURLsAndDomains(s string) []string {
	var out []string
	for _, m := range urlOrDomainPattern.FindAllString(s, -1) {
		trimmed := strings.Trim(m, ".,;:")
		if kindShapePattern.MatchString(trimmed) || ipv4Pattern.MatchString(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// enStopwords/arStopwords are small, deliberately non-exhaustive lists used
// only as a sanity check that a passage classified as "mostly the right
// script" is not simply six unrelated technical nouns that happen to share
// a script by coincidence - see languageMismatch.
var enStopwords = map[string]bool{
	"the": true, "and": true, "is": true, "was": true, "that": true, "this": true,
	"to": true, "of": true, "in": true, "a": true, "for": true, "on": true, "it": true,
	"what": true, "with": true, "an": true, "not": true, "no": true, "be": true,
}

var arStopwords = map[string]bool{
	"في": true, "من": true, "على": true, "هذا": true, "هذه": true, "التي": true,
	"كان": true, "إلى": true, "ما": true, "لا": true, "و": true, "أن": true, "مع": true,
}

// languageMismatch reports whether free-text prose is not predominantly
// written in lang. Tokens exempt from classification: handles, kind-shaped
// strings, plain numbers, IPs/MACs, and anything that already appears
// verbatim in the raw input (an entity name or hostname copied through is
// not a language choice). Of what remains, at least 90% must be letters of
// the expected script, and if six or more non-exempt tokens remain, at
// least one must be a recognised stopword - a text made almost entirely of
// exempt technical tokens is not a meaningful sample either way and is not
// flagged.
func languageMismatch(fields []string, lang, rawInput string) (reason string, bad bool) {
	wantArabic := lang == "ar"
	script := unicode.Latin
	stop := enStopwords
	if wantArabic {
		script = unicode.Arabic
		stop = arStopwords
	}

	total, matching, stopHits := 0, 0, 0
	for _, field := range fields {
		for _, tok := range strings.Fields(field) {
			trimmed := strings.Trim(tok, ".,;:!؟،()[]{}\"'")
			if trimmed == "" || isExemptToken(trimmed, rawInput) {
				continue
			}
			total++
			if tokenMatchesScript(trimmed, script) {
				matching++
			}
			if stop[strings.ToLower(trimmed)] {
				stopHits++
			}
		}
	}
	if total == 0 {
		return "", false
	}
	if float64(matching)/float64(total) < 0.9 {
		return "script mismatch", true
	}
	if total >= 6 && stopHits == 0 {
		return "no recognisable stopword in a long passage", true
	}
	return "", false
}

func isExemptToken(tok, rawInput string) bool {
	if handleShapePattern.MatchString(tok) || kindShapePattern.MatchString(tok) ||
		ipv4Pattern.MatchString(tok) || macPattern.MatchString(tok) || isAllDigits(tok) {
		return true
	}
	return rawInput != "" && strings.Contains(rawInput, tok)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func tokenMatchesScript(tok string, script *unicode.RangeTable) bool {
	letters, matches := 0, 0
	for _, r := range tok {
		if unicode.IsLetter(r) {
			letters++
			if unicode.Is(script, r) {
				matches++
			}
		}
	}
	if letters == 0 {
		return true
	}
	return matches*2 >= letters
}
