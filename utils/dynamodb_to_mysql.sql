create table yt_channels
(
    id                   int auto_increment,
    channel_id           varchar(256) not null,
    channel_name         varchar(256) not null,
    count_tracks         int          not null,
    last_upload_datetime datetime     not null,
    thumbnails           json         not null,
    upload_playlist_id   varchar(256) not null,
    constraint yt_channels_channel_id_uindex
        unique (channel_id),
    constraint yt_channels_id_uindex
        unique (id)
);

alter table yt_channels
    add primary key (id);

create table yt_playlists
(
    channel_id       varchar(256) not null,
    num              int          not null,
    count_followers  int          not null,
    count_tracks     int          not null,
    last_found_time  datetime     not null,
    last_search_time datetime     not null,
    spotify_playlist varchar(255) not null,
    primary key (channel_id, num)
);

create index yt_playlists_channel_id_num_index
    on yt_playlists (channel_id, num);

