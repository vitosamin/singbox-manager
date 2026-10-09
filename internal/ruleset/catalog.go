package ruleset

// BaseURL — база для скачивания .srs из релизов itdoginfo/allow-domains
const BaseURL = "https://github.com/itdoginfo/allow-domains/releases/latest/download/"

// CatalogItem описывает один предустановленный список.
type CatalogItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	URL         string `json:"url"`
}

// Catalog — все доступные списки (по данным репозитория itdoginfo/allow-domains).
var Catalog = []CatalogItem{
	// Медиа
	{ID: "youtube", Name: "YouTube", Description: "Видеохостинг и CDN", Category: "Медиа", URL: BaseURL + "youtube.srs"},
	{ID: "tiktok", Name: "TikTok", Description: "Соцсеть коротких видео", Category: "Медиа", URL: BaseURL + "tiktok.srs"},
	{ID: "hdrezka", Name: "HDRezka", Description: "Онлайн-кинотеатр", Category: "Медиа", URL: BaseURL + "hdrezka.srs"},
	{ID: "anime", Name: "Аниме", Description: "Аниме-ресурсы", Category: "Медиа", URL: BaseURL + "anime.srs"},
	{ID: "porn", Name: "Adult", Description: "Взрослый контент", Category: "Медиа", URL: BaseURL + "porn.srs"},

	// Соцсети
	{ID: "telegram", Name: "Telegram", Description: "Мессенджер и CDN", Category: "Соцсети", URL: BaseURL + "telegram.srs"},
	{ID: "discord", Name: "Discord", Description: "Голосовой чат и CDN", Category: "Соцсети", URL: BaseURL + "discord.srs"},
	{ID: "twitter", Name: "X (Twitter)", Description: "Соцсеть", Category: "Соцсети", URL: BaseURL + "twitter.srs"},
	{ID: "meta", Name: "Meta (FB/IG)", Description: "Facebook, Instagram, WhatsApp", Category: "Соцсети", URL: BaseURL + "meta.srs"},

	// AI
	{ID: "google_ai", Name: "Google AI", Description: "Gemini, Bard, AI Studio", Category: "AI", URL: BaseURL + "google_ai.srs"},

	// Google
	{ID: "google_meet", Name: "Google Meet", Description: "Видеоконференции", Category: "Google", URL: BaseURL + "google_meet.srs"},
	{ID: "google_play", Name: "Google Play", Description: "Магазин приложений", Category: "Google", URL: BaseURL + "google_play.srs"},

	// Игры
	{ID: "roblox", Name: "Roblox", Description: "Игровая платформа", Category: "Игры", URL: BaseURL + "roblox.srs"},

	// Гео-правила
	{ID: "russia_inside", Name: "Russia Inside", Description: "Российские ресурсы", Category: "Гео", URL: BaseURL + "russia_inside.srs"},
	{ID: "russia_outside", Name: "Russia Outside", Description: "Ресурсы РФ за рубежом", Category: "Гео", URL: BaseURL + "russia_outside.srs"},
	{ID: "geoblock", Name: "Geoblock", Description: "Гео-заблокированные ресурсы", Category: "Гео", URL: BaseURL + "geoblock.srs"},
	{ID: "block", Name: "Block", Description: "Заблокированные в РФ", Category: "Гео", URL: BaseURL + "block.srs"},
	{ID: "hodca", Name: "Hodca", Description: "Hodca-список", Category: "Гео", URL: BaseURL + "hodca.srs"},

	// Инфраструктура
	{ID: "cloudflare", Name: "Cloudflare", Description: "CDN Cloudflare", Category: "Инфра", URL: BaseURL + "cloudflare.srs"},
	{ID: "cloudfront", Name: "CloudFront", Description: "CDN Amazon", Category: "Инфра", URL: BaseURL + "cloudfront.srs"},
	{ID: "digitalocean", Name: "DigitalOcean", Description: "Хостинг", Category: "Инфра", URL: BaseURL + "digitalocean.srs"},
	{ID: "hetzner", Name: "Hetzner", Description: "Хостинг", Category: "Инфра", URL: BaseURL + "hetzner.srs"},
	{ID: "ovh", Name: "OVH", Description: "Хостинг", Category: "Инфра", URL: BaseURL + "ovh.srs"},

	// Новости
	{ID: "news", Name: "News", Description: "Новостные ресурсы", Category: "Прочее", URL: BaseURL + "news.srs"},
}

// FindByID возвращает элемент каталога по ID.
func FindByID(id string) (CatalogItem, bool) {
	for _, item := range Catalog {
		if item.ID == id {
			return item, true
		}
	}
	return CatalogItem{}, false
}
