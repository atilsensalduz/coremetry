package notify

// v0.10.1283 — "TLS doğrulamasını atla" açık Zoom kanalı HTTPS_PROXY'yi
// yok sayıyordu (sıfırdan kurulan http.Transport'ta Proxy nil). Bu test
// güvensiz transport'un proxy'yi env'den okuduğunu ve TLS doğrulamasını
// gerçekten kapattığını pinler.

import (
	"net/http"
	"net/url"
	"testing"
)

func TestZoomInsecureTransportHonoursProxy(t *testing.T) {
	tr := zoomInsecureTransport()
	if tr.Proxy == nil {
		t.Fatal("güvensiz Zoom transport'unda Proxy nil — HTTPS_PROXY yok sayılır")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify açık olmalı")
	}
	// http.ProxyFromEnvironment süreç başına bir kez env okur; fonksiyonun
	// kendisinin seçildiğini istek üzerinden doğrula (env'e bağımlı olmadan):
	// Proxy alanı DefaultTransport'unkiyle aynı davranmalı.
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.zoom.us"}}
	want, wantErr := http.DefaultTransport.(*http.Transport).Proxy(req)
	got, gotErr := tr.Proxy(req)
	if (wantErr == nil) != (gotErr == nil) || (want == nil) != (got == nil) || (want != nil && want.String() != got.String()) {
		t.Fatalf("proxy çözümü DefaultTransport'tan farklı: got %v/%v want %v/%v", got, gotErr, want, wantErr)
	}
}

func TestZoomClientForSkipVerifyUsesProxyAwareTransport(t *testing.T) {
	c := zoomClientFor(true)
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("zoomClientFor(true) proxy'siz transport döndü: %#v", c.Transport)
	}
	if zoomClientFor(false) != zoomHTTPClient {
		t.Fatal("skipVerify=false varsayılan istemciyi (DefaultTransport) kullanmalı")
	}
}
