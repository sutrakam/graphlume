defmodule Shop.Cart do
  @moduledoc "A shopping cart."

  @max_items 100
  @default_currency "EUR"
  @type t :: %__MODULE__{items: list(), total: integer()}
  @typep internal :: map()
  @opaque token :: binary()
  @callback checkout(t) :: :ok
  @spec new() :: t

  defstruct items: [], total: 0
  defguard is_empty(cart) when cart.items == []
  defguardp is_full(cart) when length(cart.items) >= 100

  use GenServer
  import Enum, only: [map: 2]
  alias Shop.Item

  def new, do: %__MODULE__{}

  def add(cart, item) when is_map(item) do
    %{cart | items: [item | cart.items]}
  end

  def add(cart, items) when is_list(items) do
    Enum.reduce(items, cart, &add(&2, &1))
  end

  def total(%__MODULE__{items: items}), do: Enum.sum(Enum.map(items, & &1.price))

  defp apply_discount(total, 0), do: total
  defp apply_discount(total, percent) do
    total - total * percent / 100
  end

  defmacro with_cart(opts, do: block) do
    quote do
      cart = unquote(opts)
      unquote(block)
    end
  end

  defmacrop private_macro(x), do: x

  defdelegate count(cart), to: Enum
  defdelegate fetch(cart, key), to: Map, as: :get

  def handle_call(:total, _from, state) do
    {:reply, total(state), state}
  end

  def handle_call({:add, item}, _from, state) do
    {:reply, :ok, add(state, item)}
  end

  def init(arg), do: {:ok, arg}

  def run(fun) do
    result = fun.()
    helper = fn x -> x * 2 end
    helper.(result)
  end

  def with_default(a, b \\ 1), do: a + b

  def unquote(:dynamic)(), do: :ok
end

defprotocol Shop.Priced do
  @doc "Returns the price."
  def price(item)
  def currency(item)
end

defimpl Shop.Priced, for: Shop.Item do
  def price(item), do: item.price
  def currency(_item), do: "EUR"
end

defmodule Shop.Item do
  defexception [:message]
  defstruct [:name, price: 0]

  defmodule Detail do
    def info(item), do: item.name
  end

  defoverridable info: 1
  @impl true
  def to_string(item), do: item.name
end

defmodule Shop.Helpers do
  defmacro __using__(_opts) do
    quote do
      import Shop.Helpers
    end
  end

  def format(amount, currency \\ "EUR")
  def format(amount, "EUR"), do: "#{amount} EUR"
  def format(amount, _), do: "#{amount}"

  # pack/1 and pack/2 are two functions; the pack/1 clause after unpack/1 is
  # not adjacent to the first, so it is a second symbol.
  def pack(item), do: item
  def pack(item, qty), do: {item, qty}
  def unpack(box), do: box
  def pack(nil), do: nil
end
