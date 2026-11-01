UPDATE archive_pages
SET media = substr(media, 1, length(media) - 4) || ':direct'
WHERE substr(media, 1, 7) = 'embyds:' AND substr(media, length(media) - 3) = ':mp4';

UPDATE archive_pages
SET media = substr(media, 1, length(media) - 4) || ':raw-mkv'
WHERE substr(media, 1, 7) = 'embyds:' AND substr(media, length(media) - 3) = ':hls';
