:- use_module(library(http/websocket)).
:- use_module(library(http/json)).
:- use_module(library(http/http_open)).
:- use_module(library(random)).
:- use_module(library(time)).

server_url('ws://localhost:8080/ws').
bot_name('PrologTerminator').

:- dynamic last_action_time/1.
action_delay(0.15). 
min_shoot_distance(5). 
chance_to_miss(15). 
chance_random_move(20). 
aggression_level(50). 

start :-
    server_url(URL),
    write('Connecting to '), writeln(URL),
    http_open_websocket(URL, WS, []),
    
    bot_name(Name),
    JoinMsg = _{type: "join", name: Name},
    ws_send(WS, json(JoinMsg)),
    
    writeln('Joined game. Waiting for init...'),
    assert(last_action_time(0)),
    game_loop(WS, _).

game_loop(WS, MyID) :-
    ws_receive(WS, Reply),
    (   Reply.opcode == close
    ->  writeln('Connection closed by server.')
    ;   Reply.opcode == text
    ->  atom_json_dict(Reply.data, Msg, []),
        handle_message(Msg, WS, MyID, NewID),
        game_loop(WS, NewID)
    ;   game_loop(WS, MyID)
    ).

handle_message(Msg, _, _, NewID) :-
    Msg.type == "init",
    atom_string(NewID, Msg.id),
    format('Bot initialized. My ID is: ~w~n', [NewID]), !.

handle_message(Msg, _, MyID, MyID) :-
    Msg.type == "dead",
    writeln('I am dead. Waiting for respawn...'), !.

handle_message(Msg, WS, MyID, MyID) :-
    Msg.type == "state",
    nonvar(MyID),
    
    get_time(Now),
    last_action_time(LastTime),
    action_delay(Delay),
    
    (   Now - LastTime >= Delay
        retract(last_action_time(_)),
        assert(last_action_time(Now)),
        
        State = Msg.state,
        Players = State.players,
        
        (   get_dict(MyID, Players, MyData),
            MyData.alive == true
        ->  choose_action(MyData, Players, Action),
            ActionMsg = _{type: "action", action: Action},
            ws_send(WS, json(ActionMsg))
        ;   true
        )
    ;
        true
    ), !.

handle_message(_, _, MyID, MyID).



choose_action(Me, Players, Action) :-
    chance_random_move(Chance),
    random(1, 101, Rand),
    (   Rand =< Chance
    ->  strategic_random_move(Me, Players, Action),
        format('Strategic random: ~w~n', [Action])
    ;
        dict_pairs(Players, _, Pairs),
        include(is_enemy(Me.id), Pairs, Enemies),
        
        (   Enemies == []
        ->  aggressive_exploration(Me, Players, Action)
        ;   % Агрессивно находим и атакуем всех врагов
            aggressive_target_selection(Me, Enemies, Target, Strategy),
            execute_strategy(Me, Target, Strategy, Action)
        )
    ).

strategic_random_move(Me, Players, Action) :-
    random(1, 101, Rand),
    (   Rand < 70
    ->  Action = "shoot",
        format('Random aggression: shooting~n')
    ;
        random_member(Action, ["up", "down", "left", "right"])
    ).

aggressive_exploration(Me, Players, Action) :-
    random(1, 101, Rand),
    (   Rand < 40 
    ->  Action = "shoot",
        writeln('Exploring: shooting randomly')
    ;
        find_best_vantage_point(Me, Players, Action)
    ).

find_best_vantage_point(Me, Players, Action) :-
    random(1, 101, Rand),
    (   Rand < 50
    ->
        CenterX = 10, CenterY = 10,
        DX is CenterX - Me.pos.x,
        DY is CenterY - Me.pos.y,
        choose_direction(DX, DY, Action),
        format('Moving to center for control~n')
    ;
        choose_flanking_move(Me, Action)
    ).

choose_flanking_move(Me, Action) :-
    MyX = Me.pos.x,
    MyY = Me.pos.y,
    (   MyX < 10
    ->  Action = "right"
    ;   Action = "left"
    ),
    format('Flanking maneuver: ~w~n', [Action]).

aggressive_target_selection(Me, Enemies, Target, Strategy) :-
    maplist(calc_dist(Me.pos), Enemies, Dists),

    keysort(Dists, Sorted),

    aggression_level(Aggro),
    random(1, 101, Rand),
    
    (   Rand < Aggro
    ->
        [Dist-Target|_] = Sorted,
        (   Dist < 5 -> Strategy = attack_close
        ;   Dist < 10 -> Strategy = attack_medium
        ;   Strategy = attack_far
        ),
        format('Aggressive: attacking at distance ~w~n', [Dist])
    ;
        find_vulnerable_target(Sorted, Target, Strategy)
    ).

find_vulnerable_target([Dist-Target|_], Target, Strategy) :-
    (   is_vulnerable(Target) 
    ->  Strategy = attack_vulnerable,
        format('Tactical: vulnerable target at distance ~w~n', [Dist])
    ;   Strategy = attack_standard,
        format('Tactical: standard attack at distance ~w~n', [Dist])
    ).

is_vulnerable(Target) :-
    true.

execute_strategy(Me, Target, Strategy, Action) :-
    DX is Target.pos.x - Me.pos.x,
    DY is Target.pos.y - Me.pos.y,
    Dist is abs(DX) + abs(DY),
    
    (   Strategy = attack_close
    ->
        (   Dist =< 3 -> aggressive_aim_and_shoot(Me.dir, DX, DY, Action)
        ;   rapid_approach(DX, DY, Action)
        )
    ;   Strategy = attack_medium
    ->
        (   (DX == 0 ; DY == 0), Dist >= 2
        ->  aggressive_aim_and_shoot(Me.dir, DX, DY, Action)
        ;   tactical_approach(DX, DY, Action)
        )
    ;   Strategy = attack_far
    ->
        rapid_approach(DX, DY, Action)
    ;   Strategy = attack_vulnerable
    ->
        guaranteed_attack(Me.dir, DX, DY, Action)
    ;
        standard_attack(Me.dir, DX, DY, Dist, Action)
    ).

aggressive_aim_and_shoot(Dir, DX, DY, Action) :-
    (   can_shoot(Dir, DX, DY)
    ->  chance_to_miss(MissChance),
        random(1, 101, Rand),
        (   Rand > MissChance
        ->  Action = "shoot",
            format('Firing shot!~n')
        ;
            random(1, 101, Rand2),
            (   Rand2 > 70 -> Action = "shoot", format('Miss chance, but firing anyway!~n')
            ;   adjust_position(DX, DY, Action), format('Miss chance, adjusting position~n')
            )
        )
    ;
        quick_turn(DX, DY, Action),
        format('Quick turn for shot~n')
    ).

quick_turn(DX, DY, Action) :-
    (   DX == 0, DY < 0 -> Action = "up"
    ;   DX == 0, DY > 0 -> Action = "down"
    ;   DY == 0, DX < 0 -> Action = "left"
    ;   DY == 0, DX > 0 -> Action = "right"
    ;
        AbsDX is abs(DX),
        AbsDY is abs(DY),
        (   AbsDX < AbsDY
        ->  (DX > 0 -> Action = "right" ; Action = "left")
        ;   (DY > 0 -> Action = "down" ; Action = "up")
        )
    ).

can_shoot(Dir, DX, DY) :-
    (   DX == 0, DY < 0, Dir.y == -1 -> true
    ;   DX == 0, DY > 0, Dir.y == 1  -> true
    ;   DY == 0, DX < 0, Dir.x == -1 -> true
    ;   DY == 0, DX > 0, Dir.x == 1  -> true
    ;   false
    ).

rapid_approach(DX, DY, Action) :-
    AbsDX is abs(DX),
    AbsDY is abs(DY),
    (   AbsDX > AbsDY
    ->  (DX > 0 -> Action = "right" ; Action = "left")
    ;   (DY > 0 -> Action = "down" ; Action = "up")
    ),
    format('Rapid approach~n').

tactical_approach(DX, DY, Action) :-
    random(1, 101, Rand),
    (   Rand < 30
    ->
        (   abs(DX) > abs(DY)
        ->  (DY > 0 -> Action = "down" ; Action = "up")
        ;   (DX > 0 -> Action = "right" ; Action = "left")
        ),
        format('Flanking approach~n')
    ;
        rapid_approach(DX, DY, Action)
    ).

guaranteed_attack(Dir, DX, DY, Action) :-
    (   can_shoot(Dir, DX, DY)
    ->  Action = "shoot",
        format('Guaranteed shot on vulnerable target!~n')
    ;
        quick_turn(DX, DY, Action),
        format('Turning for guaranteed shot~n')
    ).

standard_attack(Dir, DX, DY, Dist, Action) :-
    (   Dist >= 2, (DX == 0 ; DY == 0)
    ->  aggressive_aim_and_shoot(Dir, DX, DY, Action)
    ;   tactical_approach(DX, DY, Action)
    ).

adjust_position(DX, DY, Action) :-
    random(1, 101, Rand),
    (   Rand < 50
    ->  (abs(DX) > abs(DY) 
        ->  (DY > 0 -> Action = "down" ; Action = "up")
        ;   (DX > 0 -> Action = "right" ; Action = "left")
        )
    ;   random_member(Action, ["up", "down", "left", "right"])
    ).

choose_direction(DX, DY, Action) :-
    AbsDX is abs(DX),
    AbsDY is abs(DY),
    (   AbsDX > AbsDY
    ->  (DX > 0 -> Action = "right" ; Action = "left")
    ;   (DY > 0 -> Action = "down" ; Action = "up")
    ).

is_enemy(MyID, PlayerID-Data) :-
    atom_string(PlayerID, PlayerIDStr),
    atom_string(MyID, MyIDStr),
    PlayerIDStr \== MyIDStr,
    Data.alive == true.

calc_dist(MyPos, _-TargetData, Dist-TargetData) :-
    Dist is abs(MyPos.x - TargetData.pos.x) + abs(MyPos.y - TargetData.pos.y).
