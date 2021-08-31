drop function if exists create_ticket;

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
    _lineage_new_version         bigint;
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
            raise exception 'optimistic_lock';
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
          and released_nonce_count > 0
        returning version into _lineage_new_version;

        if _lineage_new_version is null then
            raise exception 'optimistic_lock';
        end if;
    end if;

    if _current_leased_unused_count > _max_leased_unused_count then
        raise exception 'max_unused_limit_exceeded';
    end if;

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
