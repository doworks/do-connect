package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

const testIndex = `<!DOCTYPE html>
<html lang="en">
  <head>
    <link rel="icon" type="image/svg+xml" href="./favicon.svg">
    <meta name="description" content="Whatomate - WhatsApp Business Platform">
    <title>Whatomate</title>
  </head>
  <body><div id="app"></div></body>
</html>`

const embeddedFavicon = `<svg xmlns="http://www.w3.org/2000/svg"><circle r="1"/></svg>`

const testLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><rect width="8" height="8" fill="#123456"/></svg>`

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":  {Data: []byte(testIndex)},
		"favicon.svg": {Data: []byte(embeddedFavicon)},
	}
}

func get(t *testing.T, h fasthttp.RequestHandler, path string) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(path)
	h(ctx)
	return ctx
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestRenderIndex_DefaultBrandKeepsPage(t *testing.T) {
	out := string(renderIndex([]byte(testIndex), "", Brand{}))

	assert.Contains(t, out, `<head><base href="/">`)
	assert.Contains(t, out, `<title>Whatomate</title>`)
	assert.Contains(t, out, `content="Whatomate - WhatsApp Business Platform"`)
	assert.Contains(t, out, `href="./favicon.svg"`)
	assert.Contains(t, out, `window.__BASE_PATH__ = "";`)
	assert.Contains(t, out, `window.__BRAND__ = {"logo":"","name":"Whatomate"};`)
}

func TestRenderIndex_BrandNameReplacesTitleAndDescription(t *testing.T) {
	out := string(renderIndex([]byte(testIndex), "/inbox", Brand{Name: "Acme Inbox"}))

	assert.Contains(t, out, `<title>Acme Inbox</title>`)
	assert.Contains(t, out, `content="Acme Inbox - WhatsApp Business Platform"`)
	assert.Contains(t, out, `window.__BASE_PATH__ = "/inbox";`)
	assert.Contains(t, out, `window.__BRAND__ = {"logo":"","name":"Acme Inbox"};`)
	assert.NotContains(t, out, "Whatomate")
}

func TestRenderIndex_BrandNameIsEscaped(t *testing.T) {
	name := `Acme </script><script>alert(1)</script> & "Co"`
	out := string(renderIndex([]byte(testIndex), "", Brand{Name: name}))

	assert.NotContains(t, out, "<script>alert(1)")
	assert.Equal(t, 1, strings.Count(out, "</script>"), "only the injected script may close")
	assert.Contains(t, out, `<title>Acme &lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt; &amp; &#34;Co&#34;</title>`)
	assert.Contains(t, out, `"name":"Acme \u003c/script\u003e\u003cscript\u003ealert(1)\u003c/script\u003e \u0026 \"Co\""`)
}

func TestRenderIndex_LogoAddsVersionedURL(t *testing.T) {
	logo := []byte(testLogo)
	version := logoVersion(logo)
	out := string(renderIndex([]byte(testIndex), "/inbox", Brand{Name: "Acme Inbox", Logo: logo}))

	assert.Len(t, version, 10)
	assert.Contains(t, out, `href="./favicon.svg?v=`+version+`"`)
	assert.Contains(t, out, `"logo":"/inbox/favicon.svg?v=`+version+`"`)
}

func TestHandler_ServesBrandLogo(t *testing.T) {
	h := newHandler(testFS(), "", Brand{Name: "Acme Inbox", Logo: []byte(testLogo)})
	ctx := get(t, h, "/favicon.svg?v=abc")

	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Equal(t, testLogo, string(ctx.Response.Body()))
	assert.Equal(t, "image/svg+xml", string(ctx.Response.Header.ContentType()))
	assert.Equal(t, "default-src 'none'; style-src 'unsafe-inline'", string(ctx.Response.Header.Peek("Content-Security-Policy")))
	assert.Contains(t, string(ctx.Response.Header.Peek("Cache-Control")), "max-age=")
}

func TestHandler_ServesEmbeddedFaviconWithoutLogo(t *testing.T) {
	h := newHandler(testFS(), "", Brand{Name: "Acme Inbox"})
	ctx := get(t, h, "/favicon.svg")

	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Equal(t, embeddedFavicon, string(ctx.Response.Body()))
	assert.Empty(t, ctx.Response.Header.Peek("Content-Security-Policy"))
}

func TestHandler_IndexCarriesBrand(t *testing.T) {
	h := newHandler(testFS(), "", Brand{Name: "Acme Inbox"})
	ctx := get(t, h, "/chat")

	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), `<title>Acme Inbox</title>`)
}

func TestLoadLogo(t *testing.T) {
	t.Run("valid svg", func(t *testing.T) {
		logo, err := LoadLogo(writeFile(t, "logo.svg", []byte(testLogo)))
		require.NoError(t, err)
		assert.Equal(t, testLogo, string(logo))
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadLogo(filepath.Join(t.TempDir(), "missing.svg"))
		assert.Error(t, err)
	})

	t.Run("not an svg extension", func(t *testing.T) {
		_, err := LoadLogo(writeFile(t, "logo.png", []byte(testLogo)))
		assert.Error(t, err)
	})

	t.Run("svg extension without svg content", func(t *testing.T) {
		_, err := LoadLogo(writeFile(t, "logo.svg", []byte("\x89PNG\r\n")))
		assert.Error(t, err)
	})

	t.Run("oversized", func(t *testing.T) {
		big := testLogo + "<!--" + strings.Repeat("x", maxLogoSize) + "-->"
		_, err := LoadLogo(writeFile(t, "logo.svg", []byte(big)))
		assert.Error(t, err)
	})
}

func TestHandler_ServesFilesUnderTheBasePath(t *testing.T) {
	fs := testFS()
	fs["sounds/chime.wav"] = &fstest.MapFile{Data: []byte("RIFF-chime")}
	h := newHandler(fs, "/whatomate", Brand{Name: "Acme Inbox"})

	for _, path := range []string{"/whatomate/sounds/chime.wav", "/sounds/chime.wav"} {
		ctx := get(t, h, path)
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), path)
		assert.Equal(t, "RIFF-chime", string(ctx.Response.Body()), path)
		assert.Equal(t, "audio/wav", string(ctx.Response.Header.ContentType()), path)
	}
	// Anything else under the base path is still the app's page.
	ctx := get(t, h, "/whatomate/chat/123")
	assert.Contains(t, string(ctx.Response.Body()), `window.__BASE_PATH__ = "/whatomate"`)
}
