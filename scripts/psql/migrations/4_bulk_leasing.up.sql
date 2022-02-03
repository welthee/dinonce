drop function if exists create_ticket;

create or replace function create_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_ids character varying(64)[]
) returns bigint[]
    language plpgsql
as
$$
declare
    _number_of_tickets               smallint;
    _number_of_used_released_tickets smallint;
    _nonces                          bigint[];
    _current_leased_unused_count     smallint;
    _max_leased_unused_count         smallint;
    _lineage_new_version             bigint;
    _next_nonce                      bigint;
    _now                             timestamptz;
begin
    _number_of_tickets = array_length(_ticket_ext_ids, 1);

    --
    -- being optimistic we try to get a range of new nonce, assuming:
    -- * lineage_version has not changed
    -- * no released tickets waiting to be re-assigned
    -- * leasing a new ticket does not violate max ticket count constraint
    --
    update lineages
    set next_nonce         = next_nonce + _number_of_tickets,
        version            = version + 1,
        leased_nonce_count = lineages.leased_nonce_count + _number_of_tickets
    where id = _lineage_id
      and version = _lineage_version
      and released_nonce_count = 0
    returning next_nonce, leased_nonce_count, max_leased_nonce_count into
        _next_nonce, _current_leased_unused_count, _max_leased_unused_count;

    select * from generate_series(_next_nonce - _number_of_tickets - 1, _next_nonce - 1) into _nonces;

    if _nonces is null then
        -- either optimistic lock failed or there are released tickets
        select nonce
        from released_tickets
        where lineage_id = _lineage_id
        order by lineage_id, nonce
        limit _number_of_tickets
        into _nonces;

        if _nonces is null then
            raise exception 'optimistic_lock';
        end if;

        delete
        from released_tickets
        where lineage_id = _lineage_id
          and nonce in (_nonces);

        _number_of_used_released_tickets = array_length(_nonces, 1);

        update
            lineages
        set released_nonce_count = released_nonce_count - _number_of_used_released_tickets,
            next_nonce           = next_nonce + _number_of_tickets - _number_of_used_released_tickets,
            leased_nonce_count   = leased_nonce_count + _number_of_tickets,
            version              = version + 1
        where id = _lineage_id
          and version = _lineage_version
          and released_nonce_count > _number_of_used_released_tickets
        returning version, leased_nonce_count, max_leased_nonce_count into _lineage_new_version, _current_leased_unused_count, _max_leased_unused_count;

        if _lineage_new_version is null then
            raise exception 'optimistic_lock';
        end if;
    end if;
    _now = now();
    if _number_of_used_released_tickets < _number_of_tickets then
        for i in 0.. _number_of_tickets
            loop
                insert into tickets(lineage_id, ext_id, nonce, leased_at, lease_status)
                values (_lineage_id, _ticket_ext_ids[i], _nonces[i], _now, 'leased');
            end loop;
    end if;


    if _current_leased_unused_count > _max_leased_unused_count then
        raise exception 'max_unused_limit_exceeded';
    end if;

    return _nonces;
exception
    when unique_violation then
        select nonce
        from tickets
        where lineage_id = _lineage_id
          and ext_id in (_ticket_ext_ids)
          and lease_status = 'leased'
        into _nonces;

        if _nonces is null then
            raise exception 'validation_error';
        end if;

        return _nonces;
end;
$$;