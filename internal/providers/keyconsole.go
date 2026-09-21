// internal/providers/keyconsole.go
// Purpose: where a provider's API keys are actually issued.
//
// The key prompt used to be one bare line — "Paste API key for openai (hidden):"
// — which assumes the reader already knows where to get one. Most of the time
// they do not, and the answer is a URL nobody can guess: Groq's console is not
// on groq.com's front page, and Google issues Gemini keys from AI Studio rather
// than from anything called "Gemini".
//
// Kept beside envName in this package because that is where the rest of the
// per-provider account knowledge lives, and a second table somewhere else is a
// second thing to go stale when a vendor moves a page.
package providers

import "strings"

// KeyConsoleURL returns the page where a provider issues API keys, or "" when
// there is no single such page.
//
// Returning "" rather than guessing is deliberate: a wrong URL in a prompt is
// worse than no URL, because the reader trusts it and then has to work out that
// it was Helix, not their memory, that was wrong. "custom" and the local
// runtimes genuinely have no console.
func KeyConsoleURL(provider string) string {
	// Speech providers share their chat sibling's account, so "stt.openai" is
	// asked for on the same page as "openai". Normalising here keeps the table
	// to one row per vendor.
	vendor := provider
	if _, rest, ok := strings.Cut(provider, "."); ok {
		vendor = rest
	}

	switch vendor {
	case "openai":
		return "https://platform.openai.com/api-keys"
	case "anthropic":
		return "https://console.anthropic.com/settings/keys"
	case "deepseek":
		return "https://platform.deepseek.com/api_keys"
	case "gemini":
		return "https://aistudio.google.com/apikey"
	case "groq":
		return "https://console.groq.com/keys"
	case "xai":
		return "https://console.x.ai"
	case "deepgram":
		return "https://console.deepgram.com"
	case "elevenlabs":
		return "https://elevenlabs.io/app/settings/api-keys"
	case "kimi":
		return "https://platform.moonshot.cn/console/api-keys"
	case "qwen":
		return "https://dashscope.console.aliyun.com/apiKey"
	case "glm":
		return "https://open.bigmodel.cn/usercenter/apikeys"
	case "meta":
		return "https://llama.developer.meta.com"
	}
	return ""
}

// KeyEnvName exposes the environment variable a provider's key can be set in,
// so a prompt can say "or export this instead" without the caller reaching into
// a KeyStore it does not otherwise need.
func KeyEnvName(provider string) string {
	return (&KeyStore{}).envName(provider)
}
