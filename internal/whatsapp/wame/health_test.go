package wame

import "testing"

func TestParseHealthBlockedPayment(t *testing.T) {
	body := []byte(`{"status":200,"instance":{"meta":{"phone":{"healthStatus":{"canSendMessage":"BLOCKED","entities":[
	{"entityType":"PHONE_NUMBER","canSendMessage":"LIMITED","errors":[{"code":138024,"description":"SIP"}]},
	{"entityType":"WABA","canSendMessage":"BLOCKED","errors":[{"code":141006,"description":"There is an error with the payment method.","possibleSolution":"Add a new payment method."}]},
	{"entityType":"BUSINESS","canSendMessage":"AVAILABLE"}]}}}}}`)
	h, ok, err := parseHealth(body)
	if err != nil || !ok || !h.Blocked {
		t.Fatalf("h=%+v ok=%v err=%v", h, ok, err)
	}
	if len(h.Problems) != 1 || h.Problems[0].Code != 141006 || h.Problems[0].Entity != "WABA" {
		t.Fatalf("problemas: %+v", h.Problems)
	}
}

func TestParseHealthAvailable(t *testing.T) {
	h, ok, err := parseHealth([]byte(`{"instance":{"meta":{"phone":{"healthStatus":{"canSendMessage":"AVAILABLE","entities":[]}}}}}`))
	if err != nil || !ok || h.Blocked || len(h.Problems) != 0 {
		t.Fatalf("h=%+v ok=%v err=%v", h, ok, err)
	}
	if _, ok, _ := parseHealth([]byte(`{"instance":{}}`)); ok {
		t.Fatal("sem healthStatus devia ser ok=false")
	}
}
