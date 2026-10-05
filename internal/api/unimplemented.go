package api

import "context"

// The operations no step has implemented yet. Each one answers
// 501 not_implemented; the step that implements an operation moves it from
// here to the file of its area, and DESIGN.md S20 removes this file.

// GetLibraryStatus waits for step S20.
func (Server) GetLibraryStatus(context.Context, GetLibraryStatusRequestObject) (GetLibraryStatusResponseObject, error) {
	return nil, errNotImplemented
}

// ScanLibrary waits for step S20.
func (Server) ScanLibrary(context.Context, ScanLibraryRequestObject) (ScanLibraryResponseObject, error) {
	return nil, errNotImplemented
}

// ListFavoriteTracks waits for step S18.
func (Server) ListFavoriteTracks(context.Context, ListFavoriteTracksRequestObject) (ListFavoriteTracksResponseObject, error) {
	return nil, errNotImplemented
}

// AddFavoriteTrack waits for step S18.
func (Server) AddFavoriteTrack(context.Context, AddFavoriteTrackRequestObject) (AddFavoriteTrackResponseObject, error) {
	return nil, errNotImplemented
}

// RemoveFavoriteTrack waits for step S18.
func (Server) RemoveFavoriteTrack(context.Context, RemoveFavoriteTrackRequestObject) (RemoveFavoriteTrackResponseObject, error) {
	return nil, errNotImplemented
}

// ListPlaylists waits for step S19.
func (Server) ListPlaylists(context.Context, ListPlaylistsRequestObject) (ListPlaylistsResponseObject, error) {
	return nil, errNotImplemented
}

// CreatePlaylist waits for step S19.
func (Server) CreatePlaylist(context.Context, CreatePlaylistRequestObject) (CreatePlaylistResponseObject, error) {
	return nil, errNotImplemented
}

// GetPlaylist waits for step S19.
func (Server) GetPlaylist(context.Context, GetPlaylistRequestObject) (GetPlaylistResponseObject, error) {
	return nil, errNotImplemented
}

// UpdatePlaylist waits for step S19.
func (Server) UpdatePlaylist(context.Context, UpdatePlaylistRequestObject) (UpdatePlaylistResponseObject, error) {
	return nil, errNotImplemented
}

// DeletePlaylist waits for step S19.
func (Server) DeletePlaylist(context.Context, DeletePlaylistRequestObject) (DeletePlaylistResponseObject, error) {
	return nil, errNotImplemented
}

// ListPlaylistItems waits for step S19.
func (Server) ListPlaylistItems(context.Context, ListPlaylistItemsRequestObject) (ListPlaylistItemsResponseObject, error) {
	return nil, errNotImplemented
}

// AddPlaylistItems waits for step S19.
func (Server) AddPlaylistItems(context.Context, AddPlaylistItemsRequestObject) (AddPlaylistItemsResponseObject, error) {
	return nil, errNotImplemented
}

// RemovePlaylistItem waits for step S19.
func (Server) RemovePlaylistItem(context.Context, RemovePlaylistItemRequestObject) (RemovePlaylistItemResponseObject, error) {
	return nil, errNotImplemented
}

// MovePlaylistItem waits for step S19.
func (Server) MovePlaylistItem(context.Context, MovePlaylistItemRequestObject) (MovePlaylistItemResponseObject, error) {
	return nil, errNotImplemented
}
