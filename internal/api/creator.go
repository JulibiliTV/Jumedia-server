package api

func (s *Server) registerCreator() {
	s.handle("/x/upload/web/video", s.creatorUploadVideo)
	s.handle("/x/upload/web/image", s.creatorUploadImage)
	s.handle("/x/vu/web/add/v3", s.creatorSubmit)
	s.handle("/x/vu/web/edit", s.creatorSubmit)
	s.handle("/x/web/archives", s.creatorArchives)
	s.handle("/x/web/archive/view", s.creatorView)
	s.handle("/x/web/archive/delete", s.creatorDelete)
	s.handle("/x/web/data/stat", s.creatorStat)
	s.handle("/x/web/seasons", s.creatorSeasons)
	s.handle("/x/web/season/save", s.creatorSeasonSave)
	s.handle("/x/web/season/delete", s.creatorSeasonDelete)
}
