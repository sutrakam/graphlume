%% A small module that uses every attribute form.
-module(sample).
-behaviour(gen_server).

-export([start/0, add/2, area/1]).
-export_type([shape/0, id/0]).
-import(lists, [map/2]).

-define(MAX, 100).
-define(TIMEOUT, 5000).
-define(SQUARE(X), (X) * (X)).
-define(LOG(Fmt, Args), io:format(Fmt, Args)).
-define(EMPTY, ok).

-record(point, {x = 0 :: integer(), y = 0 :: integer()}).
-record(state, {
    count = 0 :: non_neg_integer(),
    name :: binary()
}).

-type shape() :: circle | square.
-type id() :: pos_integer().
-type pair(A, B) :: {A, B}.
-opaque handle() :: reference().

-callback init(Args :: term()) -> {ok, term()}.
-callback handle(Msg :: term(),
                 State :: term()) ->
    {noreply, term()}.

-spec start() -> ok.
start() ->
    ok.

-spec add(integer(), integer()) -> integer().
add(A, B) ->
    A + B.

area({circle, R}) ->
    3 * R * R;
area({square, S}) ->
    S * S;
area(_) ->
    0.

area(Shape, Scale) ->
    area(Shape) * Scale.

-ifdef(TEST).
helper() -> ok.
-endif.

local_fun(X) ->
    F = fun(Y) -> Y + X end,
    F(1).
