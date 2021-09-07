create table if not exists lockz
(
    name                  character varying(255) primary key,
    record_version_number bigint,
    data                  bytea,
    owner                 character varying(255)
);
create sequence lockz_rvn owned by public.lockz.record_version_number;
