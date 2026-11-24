defmodule Worker.Queue do
  @behaviour GenServer
  @moduledoc false

  @timeout 5_000
  @names [:a, :b]
  @derive {Inspect, only: [:id]}
  @enforce_keys [:id]
  defstruct [:id, :payload]

  @type id :: non_neg_integer()
  @type result(t) :: {:ok, t} | {:error, term()}
  @macrocallback build(term()) :: Macro.t()

  @doc """
  Starts the queue.
  """
  @spec start_link(keyword()) :: GenServer.on_start()
  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(opts) do
    {:ok, Keyword.get(opts, :state, %{})}
  end

  @impl true
  def handle_cast({:push, item}, state) do
    {:noreply, Map.put(state, item.id, item)}
  end

  @impl true
  def handle_cast(:clear, _state), do: {:noreply, %{}}

  defp tick(state)

  defp tick(%{} = state) when map_size(state) == 0 do
    state
  end

  defp tick(
         state
       ) do
    Process.send_after(self(), :tick, @timeout)
    state
  end

  defmacro unless_empty(queue, do: block) do
    quote do
      if unquote(queue) != %{} do
        unquote(block)
      end
    end
  end

  def a, do: 1
  def b, do: 2

  if Mix.env() == :test do
    def test_only(x), do: x
  end

  for name <- @names do
    def unquote(:"name_#{name}")(), do: unquote(name)
  end

  def long_call(first, second, third) when is_integer(first) and is_integer(second) and is_integer(third) do
    first + second + third
  end
end

defmodule Worker.Application do
  use Application

  @impl Application
  def start(_type, _args) do
    children = [{Worker.Queue, []}]
    Supervisor.start_link(children, strategy: :one_for_one)
  end
end

defmodule Worker.Behaviour do
  @callback perform(term()) :: :ok | {:error, term()}
  @callback name() :: String.t()
end

defmodule Worker.QueueTest do
  use ExUnit.Case

  describe "push" do
    test "adds an item" do
      assert Worker.Queue.a() == 1
    end
  end

  setup do
    {:ok, pid: self()}
  end
end
