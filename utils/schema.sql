create table dg_genres
(
    id           int auto_increment
        primary key,
    dg_label_id  int         not null,
    genre_name   varchar(64) not null,
    count        int         not null,
    last_updated datetime    null,
    constraint dg_genres_dg_label_id_genre_name_uindex
        unique (dg_label_id, genre_name)
);

create table dg_labels
(
    id                    int auto_increment
        primary key,
    label_id              varchar(256)         not null,
    label_name            varchar(256)         not null,
    label_tracks          int        default 0 null,
    last_release_datetime datetime             null,
    thumbnail_medium      varchar(256)         null,
    added_datetime        datetime             null,
    updated_datetime      datetime             null,
    highest_dg_release    int        default 0 null,
    count_tracks          int        default 0 null,
    label_releases        int        default 0 null,
    last_page             int        default 0 null,
    did_init              tinyint(1) default 0 null,
    constraint dg_labels_label_id_uindex
        unique (label_id)
);

create table dg_playlists
(
    label_id         varchar(256) not null,
    num              int          not null,
    count_followers  int          not null,
    found_tracks     int          not null,
    last_found_time  datetime     not null,
    last_search_time datetime     not null,
    spotify_playlist varchar(255) not null,
    primary key (label_id, num)
);

create index dg_playlists_label_id_num_index
    on dg_playlists (label_id, num);

create table yt_channels
(
    id                   int auto_increment
        primary key,
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
