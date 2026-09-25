package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type proxyPurpose string

const (
	proxyPurposeRelay         proxyPurpose = "relay"
	proxyPurposeRemoteControl proxyPurpose = "remote_control"
	proxyPurposeOfficialAuth  proxyPurpose = "official_auth"
	proxyPurposeRealtime      proxyPurpose = "realtime"
	proxyPurposeModelCatalog  proxyPurpose = "model_catalog"
	proxyPurposeAudio         proxyPurpose = "audio"
	proxyPurposeVLM           proxyPurpose = "vlm"
	proxyPurposeStepwise      proxyPurpose = "stepwise"
)

const defaultProxyNoProxy = "127.0.0.1,localhost,[::1]"

func parseConfiguredProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("代理地址无效：请输入带 http:// 或 https:// 的代理地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("代理地址无效：仅支持 http:// 或 https:// 代理 URL")
	}
	if parsed.User != nil && strings.TrimSpace(parsed.User.Username()) == "" {
		return nil, fmt.Errorf("代理地址无效：代理用户名不能为空")
	}
	return parsed, nil
}

func proxyPurposeEnabled(settings backendSettings, purpose proxyPurpose) bool {
	if !settings.ProxyEnabled || strings.TrimSpace(settings.ProxyURL) == "" {
		return false
	}
	switch purpose {
	case proxyPurposeRelay:
		return settings.ProxyRelayEnabled
	case proxyPurposeRemoteControl:
		return settings.ProxyRemoteControlEnabled
	case proxyPurposeOfficialAuth:
		return settings.ProxyOfficialAuthEnabled
	case proxyPurposeRealtime:
		return settings.ProxyRealtimeEnabled
	case proxyPurposeModelCatalog:
		return settings.ProxyModelCatalogEnabled
	case proxyPurposeAudio:
		return settings.ProxyAudioEnabled
	case proxyPurposeVLM:
		return settings.ProxyVLMEnabled
	case proxyPurposeStepwise:
		return settings.ProxyStepwiseEnabled
	default:
		return false
	}
}

func nativeDesktopProxyEnabled(settings backendSettings) bool {
	return proxyPurposeEnabled(settings, proxyPurposeRemoteControl) ||
		proxyPurposeEnabled(settings, proxyPurposeOfficialAuth) ||
		proxyPurposeEnabled(settings, proxyPurposeRealtime)
}

func nativeDesktopProxyURL(settings backendSettings) (*url.URL, error) {
	for _, purpose := range []proxyPurpose{proxyPurposeRemoteControl, proxyPurposeOfficialAuth, proxyPurposeRealtime} {
		if !proxyPurposeEnabled(settings, purpose) {
			continue
		}
		return globalProxyURL(settings, purpose)
	}
	return nil, nil
}

func globalProxyURL(settings backendSettings, purpose proxyPurpose) (*url.URL, error) {
	if !proxyPurposeEnabled(settings, purpose) {
		return nil, nil
	}
	return parseConfiguredProxyURL(settings.ProxyURL)
}

func effectiveProxyURL(settings backendSettings, profile relayProfile, purpose proxyPurpose) (*url.URL, error) {
	// Native ChatGPT traffic must use the explicit global policy. In particular,
	// official Realtime must never inherit a third-party relay's proxy setting.
	if purpose == proxyPurposeRemoteControl || purpose == proxyPurposeOfficialAuth || purpose == proxyPurposeRealtime {
		return globalProxyURL(settings, purpose)
	}
	if profileURL, err := relayProfileProxyURL(profile); err != nil || profileURL != nil {
		return profileURL, err
	}
	return globalProxyURL(settings, purpose)
}

func proxyHTTPClient(settings backendSettings, profile relayProfile, purpose proxyPurpose) (*http.Client, error) {
	proxyURL, err := effectiveProxyURL(settings, profile, purpose)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		return http.DefaultClient, nil
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}, nil
	}
	transport := baseTransport.Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	return &http.Client{Transport: transport}, nil
}

func proxyEnvironmentForSettings(settings backendSettings) []string {
	if !nativeDesktopProxyEnabled(settings) {
		return nil
	}
	if _, err := parseConfiguredProxyURL(settings.ProxyURL); err != nil {
		appendDiagnosticLog("proxy.invalid_configuration", map[string]any{"error": err.Error()})
		return nil
	}
	noProxy := strings.TrimSpace(settings.ProxyNoProxy)
	if noProxy == "" {
		noProxy = defaultProxyNoProxy
	}
	value := strings.TrimSpace(settings.ProxyURL)
	return []string{
		"HTTP_PROXY=" + value,
		"HTTPS_PROXY=" + value,
		"ALL_PROXY=" + value,
		"http_proxy=" + value,
		"https_proxy=" + value,
		"all_proxy=" + value,
		"NO_PROXY=" + noProxy,
		"no_proxy=" + noProxy,
	}
}

func proxyChromiumArguments(settings backendSettings) []string {
	if !nativeDesktopProxyEnabled(settings) {
		return nil
	}
	proxyURL, err := nativeDesktopProxyURL(settings)
	if err != nil || proxyURL == nil {
		return nil
	}
	bypass := strings.TrimSpace(settings.ProxyNoProxy)
	if bypass == "" {
		bypass = defaultProxyNoProxy
	}
	bypass = strings.NewReplacer(",", ";", " ", "").Replace(bypass)
	args := []string{
		"--proxy-server=" + proxyURL.String(),
		"--proxy-bypass-list=" + bypass,
	}
	if proxyPurposeEnabled(settings, proxyPurposeRealtime) {
		// Advanced Voice uses WebRTC. Without this flag Chromium may send
		// media candidates over direct UDP even when signaling uses the proxy.
		args = append(args, "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	}
	return args
}
