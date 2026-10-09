package pagecontext

import (
	"errors"
	"net/url"
	"reflect"
	"testing"
)

const fixture = `<html><head><base href="https://shop.test/app/"><title>Shop</title></head><body>
<frameset><frame name="nav" src="nav.html"><iframe name="ads" src="/ads"></iframe></frameset>
<a href="orders?page=1">  Next
   page </a>
<a href="orders?page=2">Next page</a>
<a href="/logout">Sign &amp; out</a>
<a href="home"><img src="img/logo.png" alt="Logo"></a>
<img src="img/map.png" alt="Map" usemap="#regions">
<map name="regions"><area href="/east" alt="East"></map>
<form name="search" action="/search" method="get">
  <input name="q" value="shoes">
  <input type="checkbox" name="instock" checked>
  <input type="checkbox" name="sale" value="1">
  <select name="sort"><option value="price">Price</option><option selected>Newest</option></select>
  <input type="submit" name="go" value="Go">
</form>
<form id="login" action="login" method="POST" enctype="multipart/form-data">
  <input type="hidden" name="__VIEWSTATE" value="vs-1">
  <input name="user"><input type="password" name="pass">
  <input name="disabled" value="x" disabled>
  <textarea name="note">line1
line2</textarea>
  <input type="image" name="signin" src="img/signin.png" alt="Sign in">
</form>
</body></html>`

func parseFixture(t *testing.T) *Page {
	t.Helper()
	pageURL, _ := url.Parse("https://shop.test/start?x=1")
	return Parse([]byte(fixture), pageURL)
}

func TestParseIndexesLinksImagesFramesAndForms(t *testing.T) {
	page := parseFixture(t)
	if got := page.Base.String(); got != "https://shop.test/app/" {
		t.Fatalf("base = %s", got)
	}
	wantLinks := []Link{
		{Text: "Next page", Href: "https://shop.test/app/orders?page=1"},
		{Text: "Next page", Href: "https://shop.test/app/orders?page=2"},
		{Text: "Sign & out", Href: "https://shop.test/logout"},
		{Text: "", Href: "https://shop.test/app/home"},
	}
	if !reflect.DeepEqual(page.Links, wantLinks) {
		t.Fatalf("links = %+v", page.Links)
	}
	if len(page.Frames) != 2 || page.Frames[0] != (Frame{Name: "nav", Src: "https://shop.test/app/nav.html"}) || page.Frames[1].Src != "https://shop.test/ads" {
		t.Fatalf("frames = %+v", page.Frames)
	}
	if len(page.Images) != 3 || page.Images[0].LinkHref != "https://shop.test/app/home" || page.Images[1].UseMap != "regions" || page.Images[2].Form != 1 {
		t.Fatalf("images = %+v", page.Images)
	}
	if len(page.Areas) != 1 || page.Areas[0] != (Area{Map: "regions", Alt: "East", Href: "https://shop.test/east"}) {
		t.Fatalf("areas = %+v", page.Areas)
	}
	if len(page.Forms) != 2 {
		t.Fatalf("forms = %+v", page.Forms)
	}
	search, login := page.Forms[0], page.Forms[1]
	if search.Method != "GET" || search.Action != "https://shop.test/search" || len(search.Controls) != 5 {
		t.Fatalf("search form = %+v", search)
	}
	if login.Method != "POST" || login.Enctype != "multipart/form-data" || login.Action != "https://shop.test/app/login" || login.ID != "login" {
		t.Fatalf("login form = %+v", login)
	}
	if note := login.Controls[4]; note.Name != "note" || note.Value != "line1\nline2" {
		t.Fatalf("textarea = %+v", note)
	}
}

func TestResolution(t *testing.T) {
	page := parseFixture(t)
	if href, err := page.FindLink("Next  page", 2, false); err != nil || href != "https://shop.test/app/orders?page=2" {
		t.Fatalf("second Next page = %q, %v", href, err)
	}
	if _, err := page.FindLink("next page", 1, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("case-sensitive miss = %v", err)
	}
	if href, err := page.FindLink("next page", 1, true); err != nil || href == "" {
		t.Fatalf("case-insensitive hit = %q, %v", href, err)
	}
	if target, err := page.FindImage("Logo", "", 1, false); err != nil || target.Href != "https://shop.test/app/home" {
		t.Fatalf("linked image = %+v, %v", target, err)
	}
	if target, err := page.FindImage("", "img/map.png", 1, false); err != nil || target.Href != "https://shop.test/east" {
		t.Fatalf("single-area image map = %+v, %v", target, err)
	}
	if target, err := page.FindImage("Sign in", "", 1, false); err != nil || target.Form != 1 || target.Name != "signin" {
		t.Fatalf("input image = %+v, %v", target, err)
	}
	if !page.HasImage("", "https://shop.test/app/img/logo.png", false) || page.HasImage("Missing", "", false) {
		t.Fatal("HasImage disagrees with the index")
	}
	if index, err := page.FindForm([]string{"user", "pass"}, "", 1); err != nil || index != 1 {
		t.Fatalf("login form = %d, %v", index, err)
	}
	if _, err := page.FindForm([]string{"nope"}, "", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing form = %v", err)
	}
}

func TestSubmission(t *testing.T) {
	page := parseFixture(t)
	search := page.Forms[0].Submission([]Field{{"q", "boots"}, {"extra", "1"}}, nil, []Field{{"go", "Go"}})
	want := []Field{{"q", "boots"}, {"instock", "on"}, {"sort", "Newest"}, {"extra", "1"}, {"go", "Go"}}
	if !reflect.DeepEqual(search, want) {
		t.Fatalf("search submission = %+v", search)
	}
	login := page.Forms[1].Submission([]Field{{"user", "ada"}, {"pass", "pw"}}, []Field{{"__VIEWSTATE", "recorded"}, {"__EVENTVALIDATION", "recorded-ev"}}, []Field{{"signin.x", "0"}, {"signin.y", "0"}})
	wantLogin := []Field{{"__VIEWSTATE", "vs-1"}, {"user", "ada"}, {"pass", "pw"}, {"note", "line1\r\nline2"}, {"__EVENTVALIDATION", "recorded-ev"}, {"signin.x", "0"}, {"signin.y", "0"}}
	if !reflect.DeepEqual(login, wantLogin) {
		t.Fatalf("login submission = %+v", login)
	}
	if got := EncodeURLForm([]Field{{"a b", "c&d"}}); got != "a+b=c%26d" {
		t.Fatalf("encoding = %q", got)
	}
}
