package covers

import (
	"context"
	"database/sql"
	"errors"

	"vibrance/internal/store"
)

// warmSizes are the thumbnails made ahead of any request.
var warmSizes = [...]Size{Thumb256, Thumb640}

// Warm asks for the thumbnails of the cover with that SHA-256 to be made in
// the background, so that the first grid that shows the album finds them
// (§9.2). It only records the request and never waits: it is the
// library.CoverWarmer of the indexer, which calls it after the commit of an
// album with a new cover. Run does the work.
func (s *Service) Warm(coverSHA256 string) {
	s.mu.Lock()
	s.queue = append(s.queue, coverSHA256)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run makes the thumbnails Warm was asked for, one cover at a time, until
// ctx ends. What is still asked for then is dropped: a thumbnail that was
// not made ahead is made by the first request for it.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
		for ctx.Err() == nil {
			hash, ok := s.next()
			if !ok {
				break
			}
			s.warm(ctx, hash)
		}
	}
}

// next takes the oldest request of the queue.
func (s *Service) next() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		s.queue = nil
		return "", false
	}
	hash := s.queue[0]
	s.queue = s.queue[1:]
	return hash, true
}

// warm makes the thumbnails of one cover that the cache does not have. A
// cover no available album has any more is skipped: the album changed
// again since the request.
func (s *Service) warm(ctx context.Context, hash string) {
	var row store.GetAlbumCoverByHashRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetAlbumCoverByHash(ctx, sql.NullString{String: hash, Valid: true})
		return err
	})
	switch {
	case ctx.Err() != nil || errors.Is(err, sql.ErrNoRows):
		return
	case err != nil:
		s.log.Warn("the thumbnails were not made ahead", "cover", hash, "error", err.Error())
		return
	}
	a := album{relPath: row.RelPath, coverRel: row.CoverRel.String, sha256: hash,
		mime: row.CoverMime.String, size: row.CoverSize.Int64, mtimeNS: row.CoverMtimeNs.Int64}
	for _, size := range warmSizes {
		path, err := s.thumbPath(hash, size)
		if err == nil {
			err = s.generate(ctx, a, size, path)
		}
		switch {
		case err == nil || errors.Is(err, errTooLarge):
		case ctx.Err() != nil:
			return
		case Code(err) == CodeStale:
			// The album was replaced after its commit: the next scan
			// indexes the new one and asks again.
			s.log.Info("the thumbnails were not made ahead: the album changed", "cover", hash)
			return
		default:
			s.log.Warn("the thumbnails were not made ahead", "cover", hash, "size", int(size), "error", err.Error())
		}
	}
}
