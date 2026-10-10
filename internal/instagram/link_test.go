package instagram

import "testing"

func TestParseLink(t *testing.T) {
	cases := []struct {
		in        string
		ok        bool
		code      string
		kind      string
		canonical string
	}{
		{"https://www.instagram.com/p/C8abc_12-x/", true, "C8abc_12-x", KindPost, "https://www.instagram.com/p/C8abc_12-x/"},
		{"olha esse post https://instagram.com/p/ABC123?igsh=MWx0eXo=&utm_source=ig_web", true, "ABC123", KindPost, "https://www.instagram.com/p/ABC123/"},
		{"https://www.instagram.com/reel/Cxyz987/?igsh=abc", true, "Cxyz987", KindReel, "https://www.instagram.com/reel/Cxyz987/"},
		{"https://www.instagram.com/reels/Cxyz987/", true, "Cxyz987", KindReel, "https://www.instagram.com/reel/Cxyz987/"},
		{"https://www.instagram.com/tv/CtvCode1", true, "CtvCode1", KindTV, "https://www.instagram.com/tv/CtvCode1/"},
		{"instagram.com/p/NoScheme1/", true, "NoScheme1", KindPost, "https://www.instagram.com/p/NoScheme1/"},
		{"https://instagr.am/p/Short1/", true, "Short1", KindPost, "https://www.instagram.com/p/Short1/"},
		{"https://m.instagram.com/p/Mobile1/", true, "Mobile1", KindPost, "https://www.instagram.com/p/Mobile1/"},
		// post aberto pelo perfil: /<usuario>/p/<code>/
		{"https://www.instagram.com/pizzariaboa/p/User1code/", true, "User1code", KindPost, "https://www.instagram.com/p/User1code/"},
		{"(https://www.instagram.com/p/Paren1/).", true, "Paren1", KindPost, "https://www.instagram.com/p/Paren1/"},
		{"HTTPS://WWW.INSTAGRAM.COM/p/CaseKeep/", true, "CaseKeep", KindPost, "https://www.instagram.com/p/CaseKeep/"},

		{"https://www.instagram.com/pizzariaboa/", false, "", "", ""},
		{"https://www.instagram.com/p/", false, "", "", ""},
		{"https://www.instagram.com/stories/pizzariaboa/123/", false, "", "", ""},
		{"https://evil.com/p/ABC123/", false, "", "", ""},
		{"https://instagram.com.evil.com/p/ABC123/", false, "", "", ""},
		{"https://notinstagram.com/p/ABC123/", false, "", "", ""},
		{"https://www.instagram.com/p/bad$code/", false, "", "", ""},
		{"quero um post igual ao do instagram", false, "", "", ""},
		{"", false, "", "", ""},
	}
	for _, c := range cases {
		l, ok := ParseLink(c.in)
		if ok != c.ok {
			t.Errorf("ParseLink(%q) ok = %v, want %v (%+v)", c.in, ok, c.ok, l)
			continue
		}
		if !ok {
			continue
		}
		if l.Shortcode != c.code || l.Kind != c.kind || l.URL != c.canonical {
			t.Errorf("ParseLink(%q) = %+v, want code=%s kind=%s url=%s", c.in, l, c.code, c.kind, c.canonical)
		}
	}
}

func TestParseLinksDistinct(t *testing.T) {
	got := ParseLinks("https://www.instagram.com/p/A1/ e https://instagram.com/reel/B2/ e de novo https://www.instagram.com/p/A1/?x=1")
	if len(got) != 2 || got[0].Shortcode != "A1" || got[1].Shortcode != "B2" {
		t.Fatalf("ParseLinks = %+v", got)
	}
}
