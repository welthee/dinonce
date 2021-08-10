drop table if exists tickets;
drop table if exists released_tickets;
drop table if exists lineages;

drop type if exists ticket_lease_status;

drop sequence if exists lockz_rvn;
drop table if exists lockz;

drop function if exists create_ticket;
drop function if exists release_ticket;
drop function if exists close_ticket;

create type ticket_lease_status as enum ('leased','released', 'closed');

create table if not exists lineages
(
    id                     uuid,
    ext_id                 character varying(64),
    next_nonce             bigint,
    leased_nonce_count     smallint,
    released_nonce_count   smallint,
    max_leased_nonce_count smallint,
    max_nonce_value        bigint,
    version                bigint,
    primary key (id)
);

create unique index lineages_ext_id_idx on lineages(ext_id);

create table if not exists tickets
(
    lineage_id   uuid,
    ext_id       character varying(64),
    nonce        bigint,
    leased_at    timestamptz,
    lease_status ticket_lease_status,
    primary key (lineage_id, ext_id),
    constraint fk_lineage
        foreign key (lineage_id)
            references lineages (id)
);

create table if not exists released_tickets
(
    lineage_id  uuid,
    nonce       bigint,
    released_at timestamptz,
    primary key (lineage_id, nonce),
    constraint fk_lineage
        foreign key (lineage_id)
            references lineages (id)
);

create table if not exists lockz
(
    name                  character varying(255) primary key,
    record_version_number bigint,
    data                  bytea,
    owner                 character varying(255)
);
create sequence lockz_rvn owned by public.lockz.record_version_number;

create or replace function create_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_id character varying(64)
) returns bigint
    language plpgsql
as
$$
declare
    _nonce                       bigint;
    _current_leased_unused_count smallint;
    _max_leased_unused_count     smallint;
begin
    --
    -- being optimistic we try to get a new nonce, assuming:
    -- * lineage_version has not changed
    -- * no released tickets waiting to be re-assigned
    -- * leasing a new ticket does not violate max ticket count constraint
    --
    update lineages
    set next_nonce         = next_nonce + 1,
        version            = version + 1,
        leased_nonce_count = lineages.leased_nonce_count + 1
    where id = _lineage_id
      and version = _lineage_version
      and released_nonce_count = 0
    returning next_nonce - 1, leased_nonce_count, max_leased_nonce_count into
        _nonce, _current_leased_unused_count, _max_leased_unused_count;

    if _nonce is null then
        -- either optimistic lock failed or there are released tickets
        select nonce
        from released_tickets
        where lineage_id = _lineage_id
        order by lineage_id, nonce
        limit 1
        into _nonce;

        if _nonce is null then
            raise exception 'lineage_optimistic_lock';
        end if;

        delete
        from released_tickets
        where lineage_id = _lineage_id
          and nonce = _nonce;

        update lineages
        set released_nonce_count = released_nonce_count - 1,
            version              = version + 1
        where id = _lineage_id
          and version = _lineage_version
          and released_nonce_count > 0;
    end if;

    if _current_leased_unused_count > _max_leased_unused_count then
        raise exception 'max_unused_limit_exceeded';
    end if;

    delete
    from tickets
    where lineage_id = _lineage_id
      and ext_id = _ticket_ext_id
      and lease_status = 'released';

    insert into tickets(lineage_id, ext_id, nonce, leased_at, lease_status)
    values (_lineage_id, _ticket_ext_id, _nonce, now(), 'leased');

    return _nonce;
exception
    when unique_violation then
        select nonce
        from tickets
        where lineage_id = _lineage_id
          and ext_id = _ticket_ext_id
          and lease_status = 'leased'
        into _nonce;

        if _nonce is null then
            raise exception 'validation_error';
        end if;

        return _nonce;
end;
$$;

create or replace function release_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_id character varying(64)
) returns bigint
    language plpgsql
as
$$
declare
    _nonce      bigint;
    _now        timestamptz;
    _newversion bigint;
begin
    _now := now();

    delete from tickets
    where lineage_id = _lineage_id
      and ext_id = _ticket_ext_id
      and lease_status = 'leased'
    returning nonce into _nonce;

    if _nonce is null then
        raise exception 'no_such_ticket';
    end if;

    insert into released_tickets(lineage_id, nonce, released_at) values (_lineage_id, _nonce, _now);

    update lineages
    set released_nonce_count = released_nonce_count + 1,
        version              = version + 1
    where id = _lineage_id
      and version = _lineage_version
    returning version into _newversion;

    if _newversion is null then
        raise exception 'optimistic_lock';
    end if;

    return _nonce;
end;
$$;

create or replace function close_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_id character varying(64)
) returns void
    language plpgsql
as
$$
declare
    _now        timestamptz;
    _newversion bigint;
    _selected_ticket_ext_id character varying(64);
begin
    _now := now();

    update tickets
    set lease_status='closed'
    where lineage_id = _lineage_id
      and ext_id = _ticket_ext_id
      and lease_status = 'leased'
    returning ext_id into _selected_ticket_ext_id;

    if _selected_ticket_ext_id is null then
        raise exception 'no_such_ticket';
    end if;

    update lineages
    set leased_nonce_count = lineages.leased_nonce_count - 1,
        version            = version + 1
    where id = _lineage_id
      and version = _lineage_version
    returning version into _newversion;

    if _newversion is null then
        raise exception 'optimistic_lock';
    end if;
end;
$$;
