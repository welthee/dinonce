drop function if exists create_ticket cascade;

drop type if exists tns_triplet cascade;

create type tns_triplet as
(
    ext_id character varying(64),
    nonce  bigint,
    status text
);

create or replace function create_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_ids character varying(64)[]
) returns bigint[]
    language plpgsql
as
$$
declare
    _number_of_requested_tickets        integer;
    _existing_tickets                   tns_triplet[];
    _number_of_existing_leased_tickets  integer;
    _selected_released_nonces           bigint[];
    _number_of_selected_released_nonces integer;
    _number_of_new_tickets              integer;
    _next_nonce                         bigint;
    _number_of_leased_tickets           integer;
    _max_leased_unused_count            integer;
    _new_nonces                         bigint[];
    _new_tickets                        tns_triplet[];
    _new_ticket_ids                     character varying(64)[];
    _nonces_to_insert                   bigint[];
    _nonces_to_return                   bigint[];
    _i_ticket                           tns_triplet;
    _i_number_of_used_new_tickets       integer;
begin
    _number_of_requested_tickets = array_length(_ticket_ext_ids, 1);

    select array(
                   select (ext_id, nonce, lease_status)::tns_triplet
                   from tickets
                   where lineage_id = _lineage_id
                     and ext_id in (select(unnest(_ticket_ext_ids)))
                   order by _ticket_ext_ids)
    into _existing_tickets;

    select count(*)
    from unnest(_existing_tickets) as t
    where (t::tns_triplet).status = 'leased'
    into _number_of_existing_leased_tickets;

    if array_length(_existing_tickets, 1) != _number_of_existing_leased_tickets then
        raise exception 'validation_error';
    end if;

    select array(
                   select nonce
                   from released_tickets
                   where lineage_id = _lineage_id
                   order by lineage_id, nonce
                   limit _number_of_requested_tickets - _number_of_existing_leased_tickets
               )
    into _selected_released_nonces;

    _number_of_selected_released_nonces = array_length(_selected_released_nonces, 1);

    if _number_of_selected_released_nonces > 0 then
        delete
        from released_tickets
        where lineage_id = _lineage_id
          and nonce in (select(unnest(_selected_released_nonces)));
    end if;

    _number_of_new_tickets =
                _number_of_requested_tickets - _number_of_existing_leased_tickets - _number_of_selected_released_nonces;

    update
        lineages
    set released_nonce_count = released_nonce_count - _number_of_selected_released_nonces,
        next_nonce           = next_nonce + _number_of_new_tickets,
        leased_nonce_count   = leased_nonce_count + _number_of_new_tickets,
        version              = version + 1
    where id = _lineage_id
      and version = _lineage_version
    returning next_nonce, leased_nonce_count, max_leased_nonce_count into _next_nonce, _number_of_leased_tickets, _max_leased_unused_count;

    if _next_nonce is null then
        raise exception 'optimistic_lock';
    end if;

    if _number_of_leased_tickets > _max_leased_unused_count then
        raise exception 'max_unused_limit_exceeded';
    end if;

    select array(select * from generate_series(_next_nonce - _number_of_new_tickets, _next_nonce - 1)) into _new_nonces;

    _nonces_to_insert = array_cat(_selected_released_nonces, _new_nonces);

    select array(
                   select (t::tns_triplet).ext_id
                   from unnest(_ticket_ext_ids) as t
                   where (t::tns_triplet).ext_id not in (select unnest(_existing_tickets))
                   order by (t::tns_triplet).ext_id
               )
    into _new_ticket_ids;

    _i_number_of_used_new_tickets = array_lower(_new_tickets, 1);
    for i in array_lower(_ticket_ext_ids, 1)..array_upper(_ticket_ext_ids, 1)
        loop
            select (t::tns_triplet).ext_id, (t::tns_triplet).nonce, (t::tns_triplet).status
            from (select unnest(_existing_tickets)) as t
            where (t::tns_triplet).ext_id = _ticket_ext_ids[i]
            into _i_ticket;

            if _i_ticket is null then
                _i_ticket =
                        (_ticket_ext_ids[i], _new_nonces[_i_number_of_used_new_tickets], 'leased')::tns_triplet;
                _i_number_of_used_new_tickets = _i_number_of_used_new_tickets + 1;
            end if;

            _nonces_to_return[i] = _i_ticket.nonce;

            _i_ticket = null;
        end loop;

    return _nonces_to_return;
end
$$;