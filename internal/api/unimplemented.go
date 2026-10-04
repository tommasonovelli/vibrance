package api

import "context"

// The operations no step has implemented yet. Each one answers
// 501 not_implemented; the step that implements an operation moves it from
// here to the file of its area, and DESIGN.md S20 removes this file.

// GetServerInfo waits for step S14.
func (Server) GetServerInfo(context.Context, GetServerInfoRequestObject) (GetServerInfoResponseObject, error) {
	return nil, errNotImplemented
}

// Login waits for step S14.
func (Server) Login(context.Context, LoginRequestObject) (LoginResponseObject, error) {
	return nil, errNotImplemented
}

// CreateToken waits for step S14.
func (Server) CreateToken(context.Context, CreateTokenRequestObject) (CreateTokenResponseObject, error) {
	return nil, errNotImplemented
}

// Logout waits for step S14.
func (Server) Logout(context.Context, LogoutRequestObject) (LogoutResponseObject, error) {
	return nil, errNotImplemented
}

// GetMe waits for step S14.
func (Server) GetMe(context.Context, GetMeRequestObject) (GetMeResponseObject, error) {
	return nil, errNotImplemented
}

// ChangePassword waits for step S14.
func (Server) ChangePassword(context.Context, ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	return nil, errNotImplemented
}

// ListSessions waits for step S14.
func (Server) ListSessions(context.Context, ListSessionsRequestObject) (ListSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeSession waits for step S14.
func (Server) RevokeSession(context.Context, RevokeSessionRequestObject) (RevokeSessionResponseObject, error) {
	return nil, errNotImplemented
}

// ListUsers waits for step S14.
func (Server) ListUsers(context.Context, ListUsersRequestObject) (ListUsersResponseObject, error) {
	return nil, errNotImplemented
}

// CreateUser waits for step S14.
func (Server) CreateUser(context.Context, CreateUserRequestObject) (CreateUserResponseObject, error) {
	return nil, errNotImplemented
}

// GetUser waits for step S14.
func (Server) GetUser(context.Context, GetUserRequestObject) (GetUserResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateUser waits for step S14.
func (Server) UpdateUser(context.Context, UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteUser waits for step S14.
func (Server) DeleteUser(context.Context, DeleteUserRequestObject) (DeleteUserResponseObject, error) {
	return nil, errNotImplemented
}

// ResetUserPassword waits for step S14.
func (Server) ResetUserPassword(context.Context, ResetUserPasswordRequestObject) (ResetUserPasswordResponseObject, error) {
	return nil, errNotImplemented
}

// GetLibraryStatus waits for step S20.
func (Server) GetLibraryStatus(context.Context, GetLibraryStatusRequestObject) (GetLibraryStatusResponseObject, error) {
	return nil, errNotImplemented
}

// ScanLibrary waits for step S20.
func (Server) ScanLibrary(context.Context, ScanLibraryRequestObject) (ScanLibraryResponseObject, error) {
	return nil, errNotImplemented
}

// ListArtists waits for step S15.
func (Server) ListArtists(context.Context, ListArtistsRequestObject) (ListArtistsResponseObject, error) {
	return nil, errNotImplemented
}

// GetArtist waits for step S15.
func (Server) GetArtist(context.Context, GetArtistRequestObject) (GetArtistResponseObject, error) {
	return nil, errNotImplemented
}

// ListAlbums waits for step S15.
func (Server) ListAlbums(context.Context, ListAlbumsRequestObject) (ListAlbumsResponseObject, error) {
	return nil, errNotImplemented
}

// GetAlbum waits for step S15.
func (Server) GetAlbum(context.Context, GetAlbumRequestObject) (GetAlbumResponseObject, error) {
	return nil, errNotImplemented
}

// GetTrack waits for step S15.
func (Server) GetTrack(context.Context, GetTrackRequestObject) (GetTrackResponseObject, error) {
	return nil, errNotImplemented
}

// Search waits for step S17.
func (Server) Search(context.Context, SearchRequestObject) (SearchResponseObject, error) {
	return nil, errNotImplemented
}

// GetTrackAudio waits for step S16.
func (Server) GetTrackAudio(context.Context, GetTrackAudioRequestObject) (GetTrackAudioResponseObject, error) {
	return nil, errNotImplemented
}

// GetAlbumCover waits for step S16.
func (Server) GetAlbumCover(context.Context, GetAlbumCoverRequestObject) (GetAlbumCoverResponseObject, error) {
	return nil, errNotImplemented
}

// GetTrackLyrics waits for step S16.
func (Server) GetTrackLyrics(context.Context, GetTrackLyricsRequestObject) (GetTrackLyricsResponseObject, error) {
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
