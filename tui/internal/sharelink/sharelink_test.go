package sharelink

import (
	"strings"
	"testing"
)

const testPub = "kWJW4VtHG8oCyj9tS6k6gJmW2lX0eZzQv8xP7bYy1Kc" // not a real key pair; structure only

func TestParseNaiveLink(t *testing.T) {
	p, err := Parse("naive+https://user:pass@example.com:8443?padding=1#MyNode")
	if err != nil {
		t.Fatal(err)
	}
	if p.Server != "example.com" || p.Port != 8443 || p.Username != "user" || p.Password != "pass" {
		t.Errorf("bad profile: %+v", p)
	}
	if p.Name != "MyNode" {
		t.Errorf("name = %q", p.Name)
	}
	if p.Reality != nil {
		t.Errorf("unexpected reality block: %+v", p.Reality)
	}
}

func TestParseNaiverealLink(t *testing.T) {
	link := "naivereal://u:p@203.0.113.10:443?server_name=www.microsoft.com&public_key=" + testPub + "&short_id=ab12cd34ef56&padding=1#main"
	p, err := Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reality == nil || p.Reality.ServerName != "www.microsoft.com" || p.Reality.ShortID != "ab12cd34ef56" || p.Reality.PublicKey != testPub {
		t.Errorf("reality block wrong: %+v", p.Reality)
	}
	if p.Reality.Fingerprint != "chrome" {
		t.Errorf("fingerprint default = %q", p.Reality.Fingerprint)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse("vmess://x"); err == nil {
		t.Error("wrong scheme should fail")
	}
	if _, err := Parse("naivereal://u@host?server_name=&public_key=" + testPub + "&short_id=ab"); err == nil {
		t.Error("missing server_name should fail")
	}
	if _, err := Parse("naivereal://u@host?server_name=x.com&public_key=c2hvcnQ&short_id=ab"); err == nil {
		t.Error("short public_key should fail")
	}
	if _, err := Parse("naivereal://u@host?server_name=x.com&public_key=" + testPub + "&short_id=zz"); err == nil {
		t.Error("bad short_id should fail")
	}
}

func TestRoundtrip(t *testing.T) {
	p, err := Parse("naivereal://u:p@203.0.113.10?server_name=www.microsoft.com&public_key=" + testPub + "&short_id=abcd#main")
	if err != nil {
		t.Fatal(err)
	}
	link, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "naivereal://u:p@203.0.113.10?") {
		t.Errorf("reality build = %q", link)
	}
	p2, err := Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Reality.PublicKey != testPub || p2.Reality.ShortID != "abcd" {
		t.Errorf("roundtrip mismatch: %+v", p2.Reality)
	}

	np, err := Parse("naive+https://u:p@example.com?padding=1#n")
	if err != nil {
		t.Fatal(err)
	}
	link2, err := Build(np)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link2, "naive+https://u:p@example.com") {
		t.Errorf("naive build = %q", link2)
	}
}
