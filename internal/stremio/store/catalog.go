package stremio_store

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/stremthru/internal/cache"
	"github.com/MunifTanjim/stremthru/internal/shared"
	stremio_usenet "github.com/MunifTanjim/stremthru/internal/stremio/usenet"
	"github.com/MunifTanjim/stremthru/internal/torrent_info"
	"github.com/MunifTanjim/stremthru/internal/torrent_stream"
	"github.com/MunifTanjim/stremthru/store"
	"github.com/MunifTanjim/stremthru/stremio"
)

type CachedCatalogItem struct {
	stremio.MetaPreview
	hash string
}

var catalogCache = func() cache.Cache[[]CachedCatalogItem] {
	c := cache.NewCache[[]CachedCatalogItem](&cache.CacheConfig{
		Lifetime: 10 * time.Minute,
		Name:     "stremio:store:catalog",
	})
	return c
}()

const max_fetch_list_items = 2000
const fetch_list_limit = 500

func getUsenetCatalogItems(s store.Store, storeToken string, clientIp string, idPrefix string) []CachedCatalogItem {
	items := []CachedCatalogItem{}

	cacheKey := getCatalogCacheKey(idPrefix, storeToken)
	if !catalogCache.Get(cacheKey, &items) {
		offset := 0
		hasMore := true
		for hasMore && offset < max_fetch_list_items {
			params := &stremio_usenet.ListNewsParams{
				Limit:    fetch_list_limit,
				Offset:   offset,
				ClientIP: clientIp,
			}
			params.APIKey = storeToken
			res, err := stremio_usenet.ListNews(params, s.GetName())
			if err != nil {
				log.Error("failed to list news", "error", err, "offset", offset)
				break
			}

			for _, item := range res.Items {
				if item.Status == store.MagnetStatusDownloaded {
					cItem := CachedCatalogItem{stremio.MetaPreview{
						Id:          idPrefix + item.Id,
						Type:        ContentTypeOther,
						Name:        item.GetLargestFileName(),
						PosterShape: stremio.MetaPosterShapePoster,
					}, item.Hash}
					cItem.Description = getMetaPreviewDescriptionForUsenet(cItem.hash, item.Name, cItem.Name)
					items = append(items, cItem)
				}
			}
			offset += fetch_list_limit
			hasMore = len(res.Items) == fetch_list_limit && offset < res.TotalItems
			time.Sleep(1 * time.Second)
		}
		catalogCache.Add(cacheKey, items)
	}

	return items
}

func getCatalogItems(s store.Store, storeToken string, clientIp string, idPrefix string, isUsenet bool) []CachedCatalogItem {
	if isUsenet {
		return getUsenetCatalogItems(s, storeToken, clientIp, idPrefix)
	}

	items := []CachedCatalogItem{}

	cacheKey := getCatalogCacheKey(idPrefix, storeToken)
	if !catalogCache.Get(cacheKey, &items) {
		tInfoItems := []torrent_info.TorrentInfoInsertData{}
		tInfoSource := torrent_info.TorrentInfoSource(s.GetName().Code())

		offset := 0
		hasMore := true
		for hasMore && offset < max_fetch_list_items {
			params := &store.ListMagnetsParams{
				Limit:    fetch_list_limit,
				Offset:   offset,
				ClientIP: clientIp,
			}
			params.APIKey = storeToken
			res, err := s.ListMagnets(params)
			if err != nil {
				break
			}

			for _, item := range res.Items {
				if item.Status == store.MagnetStatusDownloaded {
					items = append(items, CachedCatalogItem{stremio.MetaPreview{
						Id:          idPrefix + item.Id,
						Type:        ContentTypeOther,
						Name:        item.Name,
						Description: getMetaPreviewDescriptionForTorrent(item.Hash, item.Name),
						PosterShape: stremio.MetaPosterShapePoster,
					}, item.Hash})
				}
				tInfoItems = append(tInfoItems, torrent_info.TorrentInfoInsertData{
					Hash:         item.Hash,
					TorrentTitle: item.Name,
					Size:         item.Size,
					Source:       tInfoSource,
				})
			}
			offset += fetch_list_limit
			hasMore = len(res.Items) == fetch_list_limit && offset < res.TotalItems
			time.Sleep(1 * time.Second)
		}
		catalogCache.Add(cacheKey, items)
		go torrent_info.Upsert(tInfoItems, "", s.GetName().Code() != store.StoreCodeRealDebrid)
	}

	return items
}

type ExtraData struct {
	Search string
	Skip   int
	Genre  string
}

func getExtra(r *http.Request) *ExtraData {
	extra := &ExtraData{}
	if extraParams := getPathParam(r, "extra"); extraParams != "" {
		if q, err := url.ParseQuery(extraParams); err == nil {
			if search := q.Get("search"); search != "" {
				extra.Search = search
			}
			if skipStr := q.Get("skip"); skipStr != "" {
				if skip, err := strconv.Atoi(skipStr); err == nil {
					extra.Skip = skip
				}
			}
			if genre := q.Get("genre"); genre != "" {
				extra.Genre = genre
			}
		}
	}
	return extra
}

func getStoreActionMetaPreview(storeCode string) stremio.MetaPreview {
	meta := stremio.MetaPreview{
		Id:   getStoreActionId(storeCode),
		Type: ContentTypeOther,
		Name: "StremThru Store Actions",
	}
	return meta
}

func getCatalogCacheKey(idPrefix, storeToken string) string {
	return idPrefix + storeToken
}

var whitespacesRegex = regexp.MustCompile(`\s+`)

func handleCatalog(w http.ResponseWriter, r *http.Request) {
	if !IsMethod(r, http.MethodGet) {
		shared.ErrorMethodNotAllowed(r).Send(w, r)
		return
	}

	ud, err := getUserData(r)
	if err != nil {
		SendError(w, r, err)
		return
	}

	if _, err := getContentType(r); err != nil {
		err.Send(w, r)
		return
	}

	catalogId := getId(r)
	idr, err := parseId(catalogId)
	if err != nil {
		SendError(w, r, err)
		return
	}

	if catalogId != getCatalogId(idr.getStoreCode()) {
		shared.ErrorBadRequest(r, "unsupported catalog id: "+catalogId).Send(w, r)
		return
	}

	ctx, err := ud.GetRequestContext(r, idr)
	if err != nil || ctx.Store == nil {
		if err != nil {
			LogError(r, "failed to get request context", err)
		}
		shared.ErrorBadRequest(r, "").Send(w, r)
		return
	}

	extra := getExtra(r)

	res := stremio.CatalogHandlerResponse{
		Metas: []stremio.MetaPreview{},
	}

	if extra.Genre == CatalogGenreStremThru {
		res.Metas = append(res.Metas, getStoreActionMetaPreview(idr.getStoreCode()))
		SendResponse(w, r, 200, res)
		return
	}

	idPrefix := getIdPrefix(idr.getStoreCode())

	items := getCatalogItems(ctx.Store, ctx.StoreAuthToken, ctx.ClientIP, idPrefix, idr.isUsenet)

	if extra.Search != "" {
		query := strings.ToLower(extra.Search)
		parts := whitespacesRegex.Split(query, -1)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		regex, err := regexp.Compile(strings.Join(parts, ".*"))
		if err != nil {
			SendError(w, r, err)
			return
		}
		filteredItems := []CachedCatalogItem{}
		for i := range items {
			item := &items[i]
			if regex.MatchString(strings.ToLower(item.Name)) {
				filteredItems = append(filteredItems, *item)
			}
		}
		items = filteredItems
	}

	limit := 100
	totalItems := len(items)
	items = items[min(extra.Skip, totalItems):min(extra.Skip+limit, totalItems)]

	hashes := make([]string, len(items))
	for i := range items {
		item := &items[i]
		hashes[i] = item.hash
	}

	res.Metas = make([]stremio.MetaPreview, len(hashes))

	stremIdByHash, err := torrent_stream.GetStremIdByHashes(hashes)
	if err != nil {
		log.Error("failed to get strem id by hashes", "error", err)
	}
	for i := range items {
		item := &items[i]
		if stremId := stremIdByHash.Get(item.hash); stremId != "" {
			stremId, _, _ = strings.Cut(stremId, ":")
			item.Poster = getPosterUrl(stremId)
		}
		res.Metas[i] = item.MetaPreview
	}

	SendResponse(w, r, 200, res)
}
