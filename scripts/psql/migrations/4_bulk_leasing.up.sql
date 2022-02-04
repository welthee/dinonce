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
    _number_of_existing_tickets      integer;
    _number_of_used_released_tickets smallint;
    _nonces                          bigint[];
    _existing_nonces                 bigint[];
    _new_nonces                      bigint[];
    _current_leased_unused_count     smallint;
    _max_leased_unused_count         smallint;
    _lineage_new_version             bigint;
    _next_nonce                      bigint;
    _now                             timestamptz;
    _i_nonce                         integer;
    _i_new_nonces                    integer;
    _temp_table_name                 text;
begin
    _number_of_tickets = array_length(_ticket_ext_ids, 1);
    _number_of_used_released_tickets = 0;

    _temp_table_name = 'existing_tickets_' || gen_random_uuid();

    create temporary table _temp_table_name on commit drop as
    select nonce, ext_id, lease_status
    from tickets
    where lineage_id = _lineage_id
      and ext_id in (select(unnest(_ticket_ext_ids)));

    if (select count(*) from _temp_table_name where lease_status != 'leased') > 0 then
        raise exception 'validation_error';
    end if;

    select count(*) from _temp_table_name into _number_of_existing_tickets;

    --
    -- being optimistic we try to get a range of new nonce, assuming:
    -- * lineage_version has not changed
    -- * no released tickets waiting to be re-assigned
    -- * leasing a new ticket does not violate max ticket count constraint
    --
    update lineages
    set next_nonce         = next_nonce + _number_of_tickets - _number_of_existing_tickets,
        version            = version + 1,
        leased_nonce_count = lineages.leased_nonce_count + _number_of_tickets - _number_of_existing_tickets
    where id = _lineage_id
      and version = _lineage_version
      and released_nonce_count = 0
    returning next_nonce, leased_nonce_count, max_leased_nonce_count into
        _next_nonce, _current_leased_unused_count, _max_leased_unused_count;

    select array(select *
                 from generate_series(_next_nonce - (_number_of_tickets - _number_of_existing_tickets),
                                      _next_nonce - 1))
    into _new_nonces;

    if array_length(_new_nonces, 1) is null then
        -- either optimistic lock failed or there are released tickets
        select array(
                       select nonce
                       from released_tickets
                       where lineage_id = _lineage_id
                       order by lineage_id, nonce
                       limit _number_of_tickets
                   )
        into _existing_nonces;

        if array_length(_existing_nonces, 1) is null then
            raise exception 'optimistic_lock';
        end if;

        delete
        from released_tickets
        where lineage_id = _lineage_id
          and nonce in (select(unnest(_existing_nonces)));

        _number_of_used_released_tickets = array_length(_existing_nonces, 1);

        update
            lineages
        set released_nonce_count = released_nonce_count - _number_of_used_released_tickets,
            next_nonce           = next_nonce + _number_of_tickets - _number_of_existing_tickets -
                                   _number_of_used_released_tickets,
            leased_nonce_count   = leased_nonce_count + _number_of_tickets - _number_of_used_released_tickets -
                                   _number_of_existing_tickets,
            version              = version + 1
        where id = _lineage_id
          and version = _lineage_version
          and released_nonce_count >= _number_of_used_released_tickets
        returning next_nonce, version, leased_nonce_count, max_leased_nonce_count into _next_nonce, _lineage_new_version, _current_leased_unused_count, _max_leased_unused_count;

        if _lineage_new_version is null then
            raise exception 'optimistic_lock';
        end if;

    end if;

    select array(select *
                 from generate_series(_next_nonce - (_number_of_tickets - _number_of_used_released_tickets),
                                      _next_nonce - 1))
    into _new_nonces;

    _now = now();
    _i_new_nonces = array_lower(_new_nonces, 1);
    for i in array_lower(_ticket_ext_ids, 1)..array_upper(_ticket_ext_ids, 1)
        loop
            select nonce
            from _temp_table_name
            where ext_id = _ticket_ext_ids[i]
            into _i_nonce;

            raise log 'ticket number %', i;
            raise log 'ticket extid %', _ticket_ext_ids[i];
            raise log 'ticket nonce %', _nonces[i];
            raise log 'ticket exists number %', _i_nonce;

            if _i_nonce is not null then
                _nonces[i] = _i_nonce;
                continue;
            end if;

            insert into tickets
                (lineage_id, ext_id, nonce, leased_at, lease_status)
            values (_lineage_id, _ticket_ext_ids[i], _new_nonces[_i_new_nonces], _now, 'leased');

            _nonces[i] = _new_nonces[_i_new_nonces];
            _i_new_nonces = _i_new_nonces + 1;
        end loop;

    raise log 'final final nonces %', _nonces;

    if _current_leased_unused_count > _max_leased_unused_count then
        raise exception 'max_unused_limit_exceeded';
    end if;

    return _nonces;
end;
$$;