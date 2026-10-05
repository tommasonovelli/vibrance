package api

import (
	"context"

	"vibrance/internal/catalog"
)

// The operations of the playlists (DESIGN.md §8.3, §8.6). A playlist is of
// the user of the request: catalog.Service answers the one of another user
// as one that does not exist. The If-Match of a request goes to the service
// as it was sent, nil without one, and is compared there, in the
// transaction of the change.

// ListPlaylists lists the playlists of the user.
func (s Server) ListPlaylists(ctx context.Context, _ ListPlaylistsRequestObject) (ListPlaylistsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	playlists, err := s.catalog.Load().ListPlaylists(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	body := PlaylistList{Playlists: make([]Playlist, 0, len(playlists))}
	for _, pl := range playlists {
		body.Playlists = append(body.Playlists, playlistOf(pl))
	}
	return ListPlaylists200JSONResponse{Body: body}, nil
}

// CreatePlaylist makes an empty playlist of the user.
func (s Server) CreatePlaylist(ctx context.Context, req CreatePlaylistRequestObject) (CreatePlaylistResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.catalog.Load().CreatePlaylist(ctx, p.UserID, req.Body.Name, req.Body.Description)
	if err != nil {
		return nil, err
	}
	return CreatePlaylist201JSONResponse{Body: playlistOf(pl), Headers: CreatePlaylist201ResponseHeaders{ETag: pl.ETag()}}, nil
}

// GetPlaylist returns a playlist of the user, without its items.
func (s Server) GetPlaylist(ctx context.Context, req GetPlaylistRequestObject) (GetPlaylistResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.catalog.Load().GetPlaylist(ctx, p.UserID, req.Id.String())
	if err != nil {
		return nil, err
	}
	return GetPlaylist200JSONResponse{Body: playlistOf(pl), Headers: GetPlaylist200ResponseHeaders{ETag: pl.ETag()}}, nil
}

// UpdatePlaylist changes the name and the description of a playlist.
func (s Server) UpdatePlaylist(ctx context.Context, req UpdatePlaylistRequestObject) (UpdatePlaylistResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.catalog.Load().UpdatePlaylist(ctx, p.UserID, req.Id.String(), req.Body.Name, req.Body.Description, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdatePlaylist200JSONResponse{Body: playlistOf(pl)}, nil
}

// DeletePlaylist deletes a playlist with its items.
func (s Server) DeletePlaylist(ctx context.Context, req DeletePlaylistRequestObject) (DeletePlaylistResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.catalog.Load().DeletePlaylist(ctx, p.UserID, req.Id.String(), req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeletePlaylist204Response{}, nil
}

// ListPlaylistItems lists the items of a playlist, by position.
func (s Server) ListPlaylistItems(ctx context.Context, req ListPlaylistItemsRequestObject) (ListPlaylistItemsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	page, err := s.catalog.Load().ListPlaylistItems(ctx, p.UserID, req.Id.String(), limitOf(req.Params.Limit), req.Params.After)
	if err != nil {
		return nil, err
	}
	body := PlaylistItemList{Items: make([]PlaylistItem, 0, len(page.Items)), Next: nextOf(page.Next)}
	for _, item := range page.Items {
		body.Items = append(body.Items, PlaylistItem{Id: item.ID, Position: item.Position, AddedAt: timestamp(item.AddedAt),
			Track: trackOf(item.Track)})
	}
	return ListPlaylistItems200JSONResponse{Body: body, Headers: ListPlaylistItems200ResponseHeaders{ETag: page.ETag}}, nil
}

// AddPlaylistItems adds tracks to a playlist, at the end or at a position.
func (s Server) AddPlaylistItems(ctx context.Context, req AddPlaylistItemsRequestObject) (AddPlaylistItemsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	trackIDs := make([]string, 0, len(req.Body.TrackIds))
	for _, id := range req.Body.TrackIds {
		trackIDs = append(trackIDs, id.String())
	}
	pl, added, err := s.catalog.Load().AddPlaylistItems(ctx, p.UserID, req.Id.String(), trackIDs, req.Body.Position, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body := AddPlaylistItemsResult{Playlist: playlistOf(pl), Added: make([]AddedPlaylistItem, 0, len(added))}
	for _, item := range added {
		body.Added = append(body.Added, AddedPlaylistItem{ItemId: item.ItemID, TrackId: item.TrackID, Position: item.Position})
	}
	return AddPlaylistItems200JSONResponse{Body: body}, nil
}

// RemovePlaylistItem removes one item of a playlist.
func (s Server) RemovePlaylistItem(ctx context.Context, req RemovePlaylistItemRequestObject) (RemovePlaylistItemResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.catalog.Load().RemovePlaylistItem(ctx, p.UserID, req.Id.String(), req.ItemId.String(), req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return RemovePlaylistItem200JSONResponse{Body: playlistOf(pl)}, nil
}

// MovePlaylistItem moves one item of a playlist to a position.
func (s Server) MovePlaylistItem(ctx context.Context, req MovePlaylistItemRequestObject) (MovePlaylistItemResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.catalog.Load().MovePlaylistItem(ctx, p.UserID, req.Id.String(), req.ItemId.String(), req.Body.Position,
		req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return MovePlaylistItem200JSONResponse{Body: playlistOf(pl)}, nil
}

func playlistOf(p catalog.Playlist) Playlist {
	return Playlist{Id: p.ID, Name: p.Name, Description: p.Description, ItemCount: p.ItemCount, DurationMs: p.DurationMS,
		Revision: p.Revision, Etag: p.ETag(), CreatedAt: timestamp(p.CreatedAt), UpdatedAt: timestamp(p.UpdatedAt)}
}
