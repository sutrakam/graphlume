library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

package counters_pkg is
  constant MAX_COUNT : natural := 255;
  type state_t is (idle, running, done);
  subtype small_t is natural range 0 to 15;
  function next_value(v : natural) return natural;
  procedure bump(signal v : inout natural);
  component adder is
    port (a, b : in  std_logic; s : out std_logic);
  end component;
end package counters_pkg;

package body counters_pkg is
  function next_value(v : natural) return natural is
  begin
    return v + 1;
  end function next_value;

  pure function "+"(a, b : small_t) return small_t is
  begin
    return a;
  end function "+";

  procedure bump(signal v : inout natural) is
    variable tmp : natural;
  begin
    tmp := next_value(v);
    v <= tmp;
  end procedure bump;
end package body counters_pkg;

library ieee;
use ieee.std_logic_1164.all;

entity counter is
  generic (WIDTH : natural := 8);
  port (
    clk   : in  std_logic;
    reset : in  std_logic;
    q     : out std_logic_vector(WIDTH - 1 downto 0)
  );
end entity counter;

architecture rtl of counter is
  signal count   : unsigned(WIDTH - 1 downto 0);
  constant LIMIT : natural := 100;
  type table_t is array (0 to 3) of natural;
  type pair_t is record
    lo : natural;
    hi : natural;
  end record;
  signal flags, mask : std_logic_vector(3 downto 0);

  function is_limit(c : unsigned) return boolean is
    function twice(n : natural) return natural is
    begin
      return n * 2;
    end function twice;
  begin
    return to_integer(c) = LIMIT;
  end function is_limit;
begin
  main_proc : process (clk, reset)
    variable local_v : natural;
  begin
    if reset = '1' then
      count <= (others => '0');
    elsif rising_edge(clk) then
      count <= count + 1;
    end if;
  end process main_proc;

  process (clk)
  begin
    null;
  end process;

  u_add : entity work.adder
    port map (a => clk, b => reset, s => open);
end architecture rtl;

configuration counter_cfg of counter is
  for rtl
  end for;
end configuration counter_cfg;
