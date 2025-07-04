package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opencensus.io/trace"
	"golang.org/x/sync/singleflight"

	"flamingo.me/flamingo/v3/framework/flamingo"
)

type (
	// HTTPLoader returns a response. it will be cached unless there is an error. this means 400/500 responses are cached too!
	HTTPLoader func(context.Context) (*http.Response, *Meta, error)

	// HTTPFrontend stores and caches http responses
	// Deprecated: Please use the dedicated httpcache flamingo module, see here: flamingo.me/httpcache
	HTTPFrontend struct {
		singleflight.Group
		backend Backend
		logger  flamingo.Logger
	}

	nopCloser struct {
		io.Reader
	}

	cachedResponse struct {
		orig *http.Response
		body []byte
	}
)

// Inject HTTPFrontend dependencies
func (hf *HTTPFrontend) Inject(backend Backend, logger flamingo.Logger) *HTTPFrontend {
	hf.backend = backend
	hf.logger = logger

	return hf
}

// GetHTTPFrontendCacheWithNullBackend helper for tests
func GetHTTPFrontendCacheWithNullBackend() *HTTPFrontend {
	return &HTTPFrontend{
		backend: &NullBackend{},
		logger:  flamingo.NullLogger{},
	}
}

// Close the nopCloser to implement io.Closer
func (nopCloser) Close() error { return nil }

func copyResponse(response cachedResponse, err error) (*http.Response, error) {
	if err != nil {
		return nil, err
	}
	var newResponse http.Response
	if response.orig != nil {
		newResponse = *response.orig
	}

	buf := make([]byte, len(response.body))
	copy(buf, response.body)
	newResponse.Body = nopCloser{bytes.NewBuffer(buf)}

	return &newResponse, nil
}

// Get a http response, with tags and a loader
// the tags will be used when the entry is stored
func (hf *HTTPFrontend) Get(ctx context.Context, key string, loader HTTPLoader) (*http.Response, error) {
	if hf.backend == nil {
		return nil, errors.New("NO backend in Cache")
	}

	ctx, span := trace.StartSpan(ctx, "flamingo/cache/httpFrontend/Get")
	span.Annotate(nil, key)
	defer span.End()

	if entry, ok := hf.backend.Get(key); ok {
		if entry.Meta.lifetime.After(time.Now()) {
			hf.logger.WithContext(ctx).
				WithField("category", "httpFrontendCache").
				Debug("Serving from cache", key)
			return copyResponse(entry.Data.(cachedResponse), nil)
		}

		if entry.Meta.gracetime.After(time.Now()) {
			go func() {
				_, _ = hf.load(ctx, key, loader, true)
			}()

			hf.logger.WithContext(ctx).
				WithField("category", "httpFrontendCache").
				Debug("Gracetime! Serving from cache", key)
			return copyResponse(entry.Data.(cachedResponse), nil)
		}
	}

	hf.logger.WithContext(ctx).
		WithField("category", "httpFrontendCache").
		Debug("No cache entry for", key)

	return copyResponse(hf.load(ctx, key, loader, false))
}

func (hf *HTTPFrontend) load(ctx context.Context, key string, loader HTTPLoader, keepExistingEntry bool) (cachedResponse, error) {
	oldSpan := trace.FromContext(ctx)
	newContext := trace.NewContext(context.Background(), oldSpan)

	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		var cancel context.CancelFunc

		newContext, cancel = context.WithDeadline(newContext, deadline)

		defer cancel()
	}

	newContextWithSpan, span := trace.StartSpan(newContext, "flamingo/cache/httpFrontend/load")
	span.Annotate(nil, key)
	defer span.End()

	data, err, _ := hf.Do(key, func() (res interface{}, resultErr error) {
		ctx, fetchRoutineSpan := trace.StartSpan(newContextWithSpan, "flamingo/cache/httpFrontend/fetchRoutine")
		fetchRoutineSpan.Annotate(nil, key)
		defer fetchRoutineSpan.End()

		defer func() {
			if err := recover(); err != nil {
				if err2, ok := err.(error); ok {
					resultErr = fmt.Errorf("httpfrontend load: %w", err2)
				} else {
					//nolint:err113 // not worth introducing a dedicated error for this edge case
					resultErr = fmt.Errorf("httpfrontend load: %v", err)
				}
			}
		}()

		data, meta, err := loader(ctx)
		if meta == nil {
			meta = &Meta{
				Lifetime:  30 * time.Second,
				Gracetime: 10 * time.Minute,
			}
		}
		if err != nil {
			return loaderResponse{nil, meta, fetchRoutineSpan.SpanContext()}, err
		}

		response := data
		body, _ := io.ReadAll(response.Body)

		response.Body.Close()

		cached := cachedResponse{
			orig: response,
			body: body,
		}

		return loaderResponse{cached, meta, fetchRoutineSpan.SpanContext()}, err
	})

	keepExistingEntry = keepExistingEntry && (err != nil || data == nil)

	response, ok := data.(loaderResponse)

	if !ok {
		data = loaderResponse{
			cachedResponse{
				orig: new(http.Response),
				body: []byte{},
			},
			&Meta{
				Lifetime:  30 * time.Second,
				Gracetime: 10 * time.Minute,
			},
			trace.SpanContext{},
		}
	}

	loadedData := response.data
	var cached cachedResponse
	if loadedData != nil {
		cached = loadedData.(cachedResponse)
	}

	if keepExistingEntry {
		//nolint:contextcheck // this log entry should be done in new context
		hf.logger.WithContext(newContextWithSpan).WithField("category", "httpFrontendCache").Debug("No store/overwrite in cache because we couldn't fetch new data", key)
	} else {
		//nolint:contextcheck // this log entry should be done in new context
		hf.logger.WithContext(newContextWithSpan).WithField("category", "httpFrontendCache").Debug("Store in Cache", key, response.meta)
		hf.backend.Set(key, &Entry{
			Data: cached,
			Meta: Meta{
				lifetime:  time.Now().Add(response.meta.Lifetime),
				gracetime: time.Now().Add(response.meta.Lifetime + response.meta.Gracetime),
				Tags:      response.meta.Tags,
			},
		})
	}

	span.AddAttributes(trace.StringAttribute("parenttrace", response.span.TraceID.String()))
	span.AddAttributes(trace.StringAttribute("parentspan", response.span.SpanID.String()))

	return cached, err
}
