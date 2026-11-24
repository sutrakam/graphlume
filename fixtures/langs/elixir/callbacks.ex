defmodule Callbacks do
  @callback one(term) :: :ok

  @callback two(msg :: term, socket :: map) ::
              {:noreply, map}
              | {:stop, term, map}

  @callback three(old, map, extra :: term) ::
              {:ok, map}
              | {:error, term}
            when old: term | {:down, term}

  @callback four(
              first :: term,
              second :: term
            ) ::
              :ok

  @callback five(a) :: a when a: term

  @callback six :: :ok

  @callback seven() ::
              :ok

  @macrocallback eight(Macro.t()) ::
                   Macro.t()

  @macrocallback nine(a) :: Macro.t() when a: term

  @optional_callbacks eight: 1

  @type pair(a) ::
          {a, a}

  @type guarded(a) :: a when a: term

  @spec local(term) :: :ok
  def local(_), do: :ok
end
