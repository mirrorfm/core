create table yt_channels
(
    id                   int auto_increment,
    channel_id           varchar(256) not null,
    channel_name         varchar(256) not null,
    count_tracks         int          not null,
    last_upload_datetime datetime     not null,
    thumbnails           json         not null,
    upload_playlist_id   varchar(256) not null,
    thumbnail_high       varchar(256) not null,
    thumbnail_medium     varchar(256) not null,
    thumbnail_default    varchar(256) not null,
    terminated_datetime  datetime     null,
    added_datetime       datetime     null,
    constraint yt_channels_channel_id_uindex
        unique (channel_id),
    constraint yt_channels_id_uindex
        unique (id)
);

alter table yt_channels
    add primary key (id);

create table yt_genres
(
    id            int auto_increment
        primary key,
    yt_channel_id int         not null,
    genre_name    varchar(64) not null,
    count         int         not null,
    last_updated  datetime    null,
    constraint yt_genres_yt_channel_id_genre_name_uindex
        unique (yt_channel_id, genre_name)
);

create table yt_playlists
(
    channel_id       varchar(256) not null,
    num              int          not null,
    count_followers  int          not null,
    found_tracks     int          not null,
    last_found_time  datetime     not null,
    last_search_time datetime     not null,
    spotify_playlist varchar(255) not null,
    primary key (channel_id, num)
);

create index yt_playlists_channel_id_num_index
    on yt_playlists (channel_id, num);

