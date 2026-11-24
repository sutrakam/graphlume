module shapes
  implicit none
  private
  public :: area, circle, scale_all

  real, parameter :: pi = 3.14159
  integer, parameter :: max_shapes = 10
  integer :: shape_count = 0

  type :: circle
    real :: radius
    character(len=8) :: label
  contains
    procedure :: perimeter => circle_perimeter
    procedure, pass :: describe
  end type circle

  type, extends(circle) :: ring
    real :: inner
  end type ring

  type, abstract :: base
  end type base

  interface area
    module procedure area_circle, area_ring
  end interface area

  interface
    function external_fn(x) result(y)
      real, intent(in) :: x
      real :: y
    end function external_fn
  end interface

contains

  function area_circle(c) result(a)
    type(circle), intent(in) :: c
    real :: a
    a = pi * c%radius**2
  end function area_circle

  pure real function area_ring(r)
    type(ring), intent(in) :: r
    area_ring = pi * (r%radius**2 - r%inner**2)
  end function area_ring

  real function circle_perimeter(self)
    class(circle), intent(in) :: self
    circle_perimeter = 2.0 * pi * self%radius
  end function circle_perimeter

  subroutine describe(self)
    class(circle), intent(in) :: self
    integer :: i
    do i = 1, 3
      print *, self%label
    end do
  end subroutine describe

  subroutine scale_all(items, factor)
    real, intent(inout) :: items(:)
    real, intent(in) :: factor
    items = items * factor
  end subroutine scale_all

  recursive function factorial(n) result(f)
    integer, intent(in) :: n
    integer :: f
    if (n <= 1) then
      f = 1
    else
      f = n * factorial(n - 1)
    end if
  end function factorial

end module shapes

submodule (shapes) shapes_impl
contains
  module subroutine extra()
    call scale_all([1.0], 2.0)
  end subroutine extra
end submodule shapes_impl

program main
  use shapes
  implicit none
  type(circle) :: c
  real :: total
  c%radius = 1.0
  total = area(c)
  call report(total)

contains

  subroutine report(v)
    real, intent(in) :: v
    print *, v
  end subroutine report
end program main

subroutine legacy_sub(a, b)
  real a, b
  b = a
end subroutine legacy_sub

integer function legacy_fn(x)
  integer x
  legacy_fn = x + 1
end function legacy_fn

block data init_data
  common /vals/ n
  data n /5/
end block data init_data

module config
  implicit none
  integer, parameter :: n_a = 1, n_b = 2
  real :: grid(10, 10)
  real :: x1, x2
  character(len=*), parameter :: banner = "hi"
end module config
