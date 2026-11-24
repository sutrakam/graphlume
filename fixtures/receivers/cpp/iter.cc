class MergingIterator : public Iterator {
 public:
  void Next() override { // def MergingIterator.Next
    current_->Next(); // call Next -> none
  }

  void Reset() { // def MergingIterator.Reset
    this->Next(); // call Next -> MergingIterator.Next high
  }

 private:
  IteratorWrapper* current_;
};
