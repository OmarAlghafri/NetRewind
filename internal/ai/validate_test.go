package ai

import "testing"

func cleanHandles() HandleMap {
	return BuildHandles([]map[string]any{{"event_id": "01M2DNFVQZP8S8ESS7C9DD9ZZD"}})
}

func cleanOutput() ModelOutput {
	return ModelOutput{
		Summary:           "The address changed hands, based on E1.",
		RankedHypotheses:  []Hypothesis{{Cause: "l2.arp_binding_changed", Entity: "10.0.0.1", Confidence: 70}},
		EvidenceHandles:   []string{"E1"},
		ConfidenceCeiling: 70,
		Unknowns:          nil,
		CounterEvidence:   nil,
		NextChecks:        []string{"Check the switch port history for E1."},
	}
}

func cleanInput() ValidateInput {
	return ValidateInput{
		Handles:      cleanHandles(),
		OfferedKinds: map[string]bool{"l2.arp_binding_changed": true},
		Ceiling:      100,
		Lang:         "en",
		RawInputText: `{"handle":"E1","kind":"l2.arp_binding_changed","subject":"10.0.0.1"} Question: what happened to 10.0.0.1?`,
	}
}

func TestValidateCleanOutputHasNoViolations(t *testing.T) {
	if v := Validate(cleanOutput(), cleanInput()); len(v) != 0 {
		t.Fatalf("a well-formed answer was flagged: %+v", v)
	}
}

func TestValidateFlagsAnUnknownHandle(t *testing.T) {
	out := cleanOutput()
	out.EvidenceHandles = []string{"E1", "E99"}
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationUnknownHandle)
	if !ViolationUnknownHandle.RetryEligible() {
		t.Error("unknown_handle must be retry-eligible")
	}
}

func TestValidateFlagsConfidenceAboveCeiling(t *testing.T) {
	out := cleanOutput()
	out.RankedHypotheses[0].Confidence = 95
	vi := cleanInput()
	vi.Ceiling = 70
	v := Validate(out, vi)
	mustHaveExactly(t, v, ViolationConfidenceAboveCeiling)
	if !ViolationConfidenceAboveCeiling.RetryEligible() {
		t.Error("confidence_above_ceiling must be retry-eligible")
	}
}

func TestValidateFlagsConfidenceCeilingFieldAboveGuardrailCeiling(t *testing.T) {
	out := cleanOutput()
	out.RankedHypotheses[0].Confidence = 60
	out.ConfidenceCeiling = 95
	vi := cleanInput()
	vi.Ceiling = 70
	v := Validate(out, vi)
	mustHaveExactly(t, v, ViolationConfidenceAboveCeiling)
}

func TestValidateFlagsACauseNotAmongTheOfferedKinds(t *testing.T) {
	out := cleanOutput()
	out.RankedHypotheses[0].Cause = "l3.route_changed" // offered kinds only has l2.arp_binding_changed
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationCauseNotOffered)
}

func TestValidateFlagsACauseInABlindFamily(t *testing.T) {
	out := cleanOutput()
	vi := cleanInput()
	vi.BlindFamilies = []string{"l2"}
	v := Validate(out, vi)
	mustHaveExactly(t, v, ViolationCauseInBlindFamily)
}

func TestValidateFlagsWrongLanguage(t *testing.T) {
	out := cleanOutput()
	out.Summary = "هذا الملخص مكتوب باللغة العربية بالكامل وليس بالإنجليزية كما طُلب."
	v := Validate(out, cleanInput()) // Lang: "en"
	mustHaveExactly(t, v, ViolationWrongLanguage)
}

func TestValidateAcceptsArabicAnswerWithLatinTechnicalTokens(t *testing.T) {
	out := cleanOutput()
	out.Summary = "تغير مالك العنوان 10.0.0.1 استنادًا إلى الحدث E1 من النوع l2.arp_binding_changed."
	out.NextChecks = []string{"تحقق من سجل منفذ المحول للحدث E1."}
	vi := cleanInput()
	vi.Lang = "ar"
	vi.RawInputText = `{"handle":"E1","kind":"l2.arp_binding_changed","subject":"10.0.0.1"} Question: ماذا حدث؟`
	if v := Validate(out, vi); len(v) != 0 {
		t.Errorf("a correct Arabic answer with exempt Latin technical tokens (handle, kind, IP) was flagged: %+v", v)
	}
}

func TestValidateRejectsEnglishAnswerToAnArabicQuestion(t *testing.T) {
	out := cleanOutput() // English summary/next_checks
	vi := cleanInput()
	vi.Lang = "ar"
	v := Validate(out, vi)
	mustHaveExactly(t, v, ViolationWrongLanguage)
}

func TestValidateFlagsAFabricatedURL(t *testing.T) {
	out := cleanOutput()
	out.NextChecks = []string{"See https://totally-invented-support-site.example/help for more."}
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationFabricatedURLOrDomain)
}

func TestValidateAcceptsAURLThatWasActuallyInTheInput(t *testing.T) {
	out := cleanOutput()
	out.NextChecks = []string{"The DNS answer named example.com."}
	vi := cleanInput()
	vi.RawInputText += " example.com"
	if v := Validate(out, vi); len(v) != 0 {
		t.Errorf("a domain that was genuinely present in the input was flagged as fabricated: %+v", v)
	}
}

func TestValidateDoesNotFlagKindOrIPShapedStringsAsDomains(t *testing.T) {
	out := cleanOutput()
	out.Summary = "Caused by l2.arp_binding_changed on 10.0.0.1, cited in E1."
	if v := Validate(out, cleanInput()); len(v) != 0 {
		t.Errorf("a kind string and an IP address were misidentified as fabricated domains: %+v", v)
	}
}

func TestValidateFlagsCommandSyntax(t *testing.T) {
	out := cleanOutput()
	out.NextChecks = []string{"Run ping -t 10.0.0.1 to confirm."}
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationCommandSyntax)
}

func TestValidateFlagsAShellOperator(t *testing.T) {
	out := cleanOutput()
	out.Summary = "the address changed hands; rm -rf / && echo done"
	v := Validate(out, cleanInput())
	if !hasViolation(v, ViolationCommandSyntax) {
		t.Errorf("a shell operator was not flagged as command_syntax: %+v", v)
	}
}

func TestValidateDoesNotFlagABareToolName(t *testing.T) {
	out := cleanOutput()
	out.NextChecks = []string{"Consider running a ping test to the gateway."}
	if v := Validate(out, cleanInput()); len(v) != 0 {
		t.Errorf("a bare mention of a tool name with no flag/operator was flagged: %+v", v)
	}
}

func TestValidateFlagsAnActionClaim(t *testing.T) {
	out := cleanOutput()
	out.Summary = "I have disabled the offending interface to stop the loop."
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationActionClaim)
}

func TestValidateFlagsAnArabicActionClaim(t *testing.T) {
	out := cleanOutput()
	out.Summary = "تم إيقاف الواجهة المتسببة في المشكلة."
	vi := cleanInput()
	vi.Lang = "ar"
	v := Validate(out, vi)
	if !hasViolation(v, ViolationActionClaim) {
		t.Errorf("an Arabic action claim was not detected: %+v", v)
	}
}

func TestValidateFlagsActiveMarkup(t *testing.T) {
	out := cleanOutput()
	out.Summary = "See <script>alert(1)</script> for details."
	v := Validate(out, cleanInput())
	if !hasViolation(v, ViolationActiveMarkup) {
		t.Errorf("an embedded script tag was not flagged: %+v", v)
	}
}

func TestValidateFlagsAPhantomHandleInFreeText(t *testing.T) {
	out := cleanOutput()
	out.Summary = "Also consider E7, which was not offered."
	v := Validate(out, cleanInput())
	if !hasViolation(v, ViolationPhantomHandle) {
		t.Errorf("a phantom handle in free text was not flagged: %+v", v)
	}
}

func TestValidateFlagsARawULIDInFreeText(t *testing.T) {
	out := cleanOutput()
	out.Summary = "See event 01M2DNFVQZP8S8ESS7C9DD9ZZD directly."
	v := Validate(out, cleanInput())
	mustHaveExactly(t, v, ViolationRawIDInText)
}

func TestValidateFlagsRefusalShapeWhenNothingIsOfferedOrUnknown(t *testing.T) {
	out := ModelOutput{ConfidenceCeiling: 0}
	v := Validate(out, cleanInput())
	if !hasViolation(v, ViolationRefusalShape) {
		t.Errorf("an empty answer with no hypotheses and no unknowns was not flagged: %+v", v)
	}
}

func TestValidateAcceptsAProperRefusal(t *testing.T) {
	out := ModelOutput{Unknowns: []string{"The recorder has no coverage for this window."}}
	if v := Validate(out, cleanInput()); len(v) != 0 {
		t.Errorf("a proper refusal (empty hypotheses, non-empty unknowns) was flagged: %+v", v)
	}
}

func TestAllViolationCodesHasNoDuplicatesAndCoversEveryRetryEligibleCode(t *testing.T) {
	seen := map[ViolationCode]bool{}
	for _, c := range AllViolationCodes {
		if seen[c] {
			t.Errorf("duplicate code in AllViolationCodes: %s", c)
		}
		seen[c] = true
	}
	for c := range retryEligibleCodes {
		if !seen[c] {
			t.Errorf("retry-eligible code %s is missing from AllViolationCodes", c)
		}
	}
}

func mustHaveExactly(t *testing.T, v []Violation, code ViolationCode) {
	t.Helper()
	if len(v) != 1 || v[0].Code != code {
		t.Fatalf("violations = %+v, want exactly one %s", v, code)
	}
}

func hasViolation(v []Violation, code ViolationCode) bool {
	for _, x := range v {
		if x.Code == code {
			return true
		}
	}
	return false
}
